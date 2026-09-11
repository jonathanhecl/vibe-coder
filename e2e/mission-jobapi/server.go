// Command server is a tiny, self-contained async job API used to exercise the
// mission runtime end-to-end without ComfyUI. It mimics the shape of an image
// generation service: POST a job, poll until it finishes, then fetch the
// result. A configurable fraction of first attempts fail so the agent must
// retry, and each job reports "running" for a few polls so the agent must poll.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
)

type job struct {
	ID        string
	Prompt    string
	Status    string // running | done | failed
	Final     string // done | failed
	TicksLeft int
}

type server struct {
	mu         sync.Mutex
	jobs       map[string]*job
	seq        int
	submits    int
	polls      int
	results    int
	failedJobs int
	attempts   map[string]int
	pollTicks  int
	failEvery  int
}

func newServer(pollTicks, failEvery int) *server {
	return &server{
		jobs:      map[string]*job{},
		attempts:  map[string]int{},
		pollTicks: pollTicks,
		failEvery: failEvery,
	}
}

func (s *server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}
	s.mu.Lock()
	s.submits++
	s.seq++
	id := fmt.Sprintf("job-%d", s.seq)
	s.attempts[body.Prompt]++
	attempt := s.attempts[body.Prompt]
	fail := s.failEvery > 0 && attempt == 1 && s.seq%s.failEvery == 0
	final := "done"
	if fail {
		final = "failed"
		s.failedJobs++
	}
	j := &job{ID: id, Prompt: body.Prompt, Status: "running", Final: final, TicksLeft: s.pollTicks}
	s.jobs[id] = j
	s.mu.Unlock()

	log.Printf("submit %s prompt=%q attempt=%d final=%s", id, body.Prompt, attempt, final)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "running"})
}

func (s *server) handleJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "job id required"})
		return
	}
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
		return
	}
	if j.Status == "running" {
		s.polls++
		j.TicksLeft--
		if j.TicksLeft <= 0 {
			j.Status = j.Final
		}
	}
	status := j.Status
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
}

func (s *server) handleResult(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/jobs/")
	id := strings.TrimSuffix(path, "/result")
	if id == "" || id == path {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "use /jobs/{id}/result"})
		return
	}
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
		return
	}
	status := j.Status
	prompt := j.Prompt
	if status == "done" {
		s.results++
	}
	s.mu.Unlock()

	switch status {
	case "running":
		writeJSON(w, http.StatusTooEarly, map[string]any{"error": "job still running"})
	case "failed":
		writeJSON(w, http.StatusConflict, map[string]any{"error": "job failed"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "prompt": prompt, "value": prompt + "-ok"})
	}
}

func (s *server) handleStats(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"submits":     s.submits,
		"polls":       s.polls,
		"results":     s.results,
		"failed_jobs": s.failedJobs,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8799", "listen address")
	pollTicks := flag.Int("poll-ticks", 2, "how many polls a job stays running")
	failEvery := flag.Int("fail-every", 3, "fail the first attempt of every Nth submitted job (0 disables)")
	flag.Parse()

	s := newServer(*pollTicks, *failEvery)
	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", s.handleSubmit)
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/result") {
			s.handleResult(w, r)
			return
		}
		s.handleJob(w, r)
	})
	mux.HandleFunc("/_stats", s.handleStats)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown path", "path": r.URL.Path})
	})

	log.Printf("job API listening on %s (poll-ticks=%d fail-every=%d)", *addr, *pollTicks, *failEvery)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
