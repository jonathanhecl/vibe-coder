package session

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// archiveTestConfig builds a config whose tiny context window forces
// compaction to archive most of the transcript, with the sidecar fake
// supplying the summary text.
func archiveTestConfig(t *testing.T, isolated bool) *config.Config {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		ContextWindow: 256,
		SidecarModel:  "sidecar",
	}
	if isolated {
		cfg.Isolated = true
	}
	return cfg
}

// fillTranscript adds a distinctive first user prompt followed by filler
// user turns, enough for a small keepTail to push most of them into old.
func fillTranscript(s *Session, first string, fillers int) {
	s.AddUser(first)
	for i := 0; i < fillers; i++ {
		s.AddUser("filler-" + strings.Repeat("x", 30) + string(rune('a'+i%26)))
	}
}

// readSessionLines returns the raw JSONL lines of a saved session file.
func readSessionLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open session file: %v", err)
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionLineBytes)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan session file: %v", err)
	}
	return lines
}

// assertArchiveRoundTrip checks the invariants every archive save/load must
// keep: archived lines precede working lines on disk, the working view
// survives the round trip unchanged, and the archive keeps its order.
func assertArchiveRoundTrip(t *testing.T, before *Session, after *Session, fileLines []string) {
	t.Helper()

	// On disk: every archived message line comes before the first working
	// (non-archived) message line.
	seenWorking := false
	archivedOnDisk := 0
	for _, line := range fileLines {
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.Role == "" {
			continue // isolated files start with a _meta line
		}
		if msg.Archived {
			if seenWorking {
				t.Fatal("archived message found after a working message on disk")
			}
			archivedOnDisk++
		} else {
			seenWorking = true
		}
	}
	if archivedOnDisk != before.ArchivedCount() {
		t.Fatalf("disk archived lines = %d, want %d", archivedOnDisk, before.ArchivedCount())
	}
	if !strings.Contains(strings.Join(fileLines, "\n"), `"archived":true`) {
		t.Fatal("session file is missing \"archived\":true markers")
	}

	// The working view is identical before save and after load.
	beforeMsgs := before.Messages()
	afterMsgs := after.Messages()
	if len(beforeMsgs) != len(afterMsgs) {
		t.Fatalf("working view length changed across save/load: %d -> %d", len(beforeMsgs), len(afterMsgs))
	}
	for i := range beforeMsgs {
		if beforeMsgs[i].Role != afterMsgs[i].Role || beforeMsgs[i].Content != afterMsgs[i].Content {
			t.Fatalf("working view message %d changed across save/load: %+v vs %+v", i, beforeMsgs[i], afterMsgs[i])
		}
	}

	// The archive round-trips with the same size and order.
	if after.ArchivedCount() != before.ArchivedCount() {
		t.Fatalf("ArchivedCount = %d, want %d", after.ArchivedCount(), before.ArchivedCount())
	}
	beforeFull := before.FullTranscript()
	afterFull := after.FullTranscript()
	if len(beforeFull) != len(afterFull) {
		t.Fatalf("FullTranscript length = %d, want %d", len(afterFull), len(beforeFull))
	}
	for i := range beforeFull {
		if beforeFull[i].Role != afterFull[i].Role || beforeFull[i].Content != afterFull[i].Content ||
			beforeFull[i].Archived != afterFull[i].Archived || beforeFull[i].Anchor != afterFull[i].Anchor {
			t.Fatalf("FullTranscript message %d changed across save/load: %+v vs %+v", i, beforeFull[i], afterFull[i])
		}
	}
}

// TestCompactArchivesAndSaveLoadRoundTrip verifies a normal session keeps the
// full transcript on disk: compacted messages are archived in order, the
// working view the model sees is unchanged, and a reload restores both.
func TestCompactArchivesAndSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	cfg := archiveTestConfig(t, false)
	s := New(cfg)
	s.SetClient(sidecarClient{})

	fillTranscript(s, "Build the widget", 40)
	if err := s.CompactTo(context.Background(), 30); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if s.ArchivedCount() == 0 {
		t.Fatal("expected compaction to archive messages")
	}

	// The first user prompt survives twice by design: once archived, once as
	// the Anchor-flagged copy in the working view. It must not be archived
	// a second time.
	archivedFirstUser := 0
	anchorCopies := 0
	for _, m := range s.FullTranscript() {
		if m.Content != "Build the widget" {
			continue
		}
		if m.Anchor {
			anchorCopies++
		} else if m.Archived {
			archivedFirstUser++
		}
	}
	if archivedFirstUser != 1 {
		t.Fatalf("archived copies of first user prompt = %d, want 1", archivedFirstUser)
	}
	if anchorCopies != 1 {
		t.Fatalf("anchor copies of first user prompt = %d, want 1", anchorCopies)
	}
	for _, m := range s.FullTranscript() {
		if m.Archived && m.Anchor {
			t.Fatal("archive must never contain an anchor copy")
		}
	}

	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	fileLines := readSessionLines(t, filepath.Join(cfg.SessionsDir, s.ID()+".jsonl"))

	s2 := New(cfg)
	if err := s2.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	assertArchiveRoundTrip(t, s, s2, fileLines)
}

// TestCompactTwiceKeepsBothSummariesOnDisk verifies that a second compaction
// archives the first summary (so every condensation stays on disk in order)
// while the newest summary remains in the working view.
func TestCompactTwiceKeepsBothSummariesOnDisk(t *testing.T) {
	t.Parallel()
	cfg := archiveTestConfig(t, false)
	s := New(cfg)
	s.SetClient(sidecarClient{})

	fillTranscript(s, "original goal", 40)
	if err := s.CompactTo(context.Background(), 30); err != nil {
		t.Fatalf("first compact: %v", err)
	}
	fillTranscript(s, "mid-run prompt", 40)
	if err := s.CompactTo(context.Background(), 30); err != nil {
		t.Fatalf("second compact: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	fileLines := readSessionLines(t, filepath.Join(cfg.SessionsDir, s.ID()+".jsonl"))
	joined := strings.Join(fileLines, "\n")
	if got := strings.Count(joined, "[Earlier conversation summary]"); got != 2 {
		t.Fatalf("session file holds %d summaries, want 2 (one archived, one working)", got)
	}

	s2 := New(cfg)
	if err := s2.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	assertArchiveRoundTrip(t, s, s2, fileLines)

	// Chronology: the first summary must sit inside the archived region,
	// before the second one which stays in the working view.
	archivedSummary, workingSummary := -1, -1
	for i, line := range fileLines {
		if !strings.Contains(line, "[Earlier conversation summary]") {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("decode summary line: %v", err)
		}
		if msg.Archived && archivedSummary < 0 {
			archivedSummary = i
		}
		if !msg.Archived && workingSummary < 0 {
			workingSummary = i
		}
	}
	if archivedSummary < 0 || workingSummary < 0 || archivedSummary > workingSummary {
		t.Fatalf("summary order on disk wrong: archived=%d working=%d", archivedSummary, workingSummary)
	}
}

// TestIsolatedCompactArchivesAndRoundTrip runs the same archive flow through
// the isolated session file, including its _meta header line.
func TestIsolatedCompactArchivesAndRoundTrip(t *testing.T) {
	t.Parallel()
	cfg := archiveTestConfig(t, true)
	s := New(cfg)
	s.SetClient(sidecarClient{})

	fillTranscript(s, "isolated goal", 40)
	if err := s.CompactTo(context.Background(), 30); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save isolated: %v", err)
	}
	fileLines := readSessionLines(t, filepath.Join(cfg.Cwd, IsolatedSessionFileName))

	s2 := New(cfg)
	ok, err := s2.LoadIsolated()
	if err != nil {
		t.Fatalf("load isolated: %v", err)
	}
	if !ok {
		t.Fatal("LoadIsolated returned false")
	}
	assertArchiveRoundTrip(t, s, s2, fileLines)
}

// TestLegacySessionLoadsWithoutArchive verifies a pre-archive JSONL (no
// archived fields) loads entirely into the working view.
func TestLegacySessionLoadsWithoutArchive(t *testing.T) {
	t.Parallel()
	cfg := archiveTestConfig(t, false)
	probe := New(cfg)
	id := probe.ID()

	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.SessionsDir, id+".jsonl")
	var b strings.Builder
	for _, content := range []string{"first", "second", "third"} {
		line, err := json.Marshal(Message{Role: "user", Content: content})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(cfg)
	if err := s.Load(id); err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	if s.ArchivedCount() != 0 {
		t.Fatalf("ArchivedCount = %d, want 0 for a legacy file", s.ArchivedCount())
	}
	if s.MessageCount() != 3 {
		t.Fatalf("MessageCount = %d, want 3", s.MessageCount())
	}
	if got := len(s.FullTranscript()); got != 3 {
		t.Fatalf("FullTranscript length = %d, want 3", got)
	}
}

// TestClearEmptiesArchive verifies /new-style resets drop the archive with
// the working view.
func TestClearEmptiesArchive(t *testing.T) {
	t.Parallel()
	cfg := archiveTestConfig(t, false)
	s := New(cfg)
	s.SetClient(sidecarClient{})

	fillTranscript(s, "doomed", 40)
	if err := s.CompactTo(context.Background(), 30); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if s.ArchivedCount() == 0 {
		t.Fatal("expected archive to be non-empty before clear")
	}
	s.Clear()
	if s.ArchivedCount() != 0 {
		t.Fatalf("ArchivedCount after Clear = %d, want 0", s.ArchivedCount())
	}
	if s.MessageCount() != 0 {
		t.Fatalf("MessageCount after Clear = %d, want 0", s.MessageCount())
	}
}

// TestUserPromptsFilters verifies prompt recall sees only real user prompts:
// tool observations, runtime reminders, summaries, mission notes, blank
// lines and retry scaffolding are all skipped.
func TestUserPromptsFilters(t *testing.T) {
	t.Parallel()
	s := New(archiveTestConfig(t, false))

	s.AddUser("first real prompt")
	s.AddUser("second prompt\n\n[retry_context]\nnoise\n[/retry_context]")
	s.AddUser("alpha\nbeta")
	s.AddToolObservation("Read", "file contents")
	s.AddRuntimeReminder("keep working on the TODO list")
	s.AddUser("[Earlier conversation summary]\nold news")
	s.AddUser("[mission] finish the refactor")
	s.AddUser("[RAG Context] retrieved chunk")
	s.AddUser("   ")

	got := s.UserPrompts()
	want := []string{"first real prompt", "second prompt", "alpha\nbeta"}
	if len(got) != len(want) {
		t.Fatalf("UserPrompts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("UserPrompts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestUserPromptsSkipsAnchorCopy verifies the preserved first-user copy in
// the working view does not duplicate the archived original in prompt recall.
func TestUserPromptsSkipsAnchorCopy(t *testing.T) {
	t.Parallel()
	cfg := archiveTestConfig(t, false)
	s := New(cfg)
	s.SetClient(sidecarClient{})

	fillTranscript(s, "the original ask", 40)
	if err := s.CompactTo(context.Background(), 30); err != nil {
		t.Fatalf("compact: %v", err)
	}

	count := 0
	for _, p := range s.UserPrompts() {
		if p == "the original ask" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("UserPrompts lists the original ask %d times, want 1", count)
	}
}
