package jevstylev3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/jevstyle"
)

// newMockV3Server creates a test server that simulates JEV v3 responses.
func newMockV3Server(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL), srv
}

func TestEnabled(t *testing.T) {
	c := New("http://localhost:8765")
	if !c.Enabled() {
		t.Fatal("expected enabled for configured endpoint")
	}
	c2 := New("")
	if c2.Enabled() {
		t.Fatal("expected disabled for empty endpoint")
	}
	var nilClient *Client
	if nilClient.Enabled() {
		t.Fatal("expected disabled for nil client")
	}
}

func TestModel(t *testing.T) {
	c := New("http://192.168.0.33:8765")
	if c.Model() != "http://192.168.0.33:8765" {
		t.Fatalf("expected endpoint, got %q", c.Model())
	}
}

func TestHealthCheck_Success(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("expected /healthz, got %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"model":  "jev-style-2b-decision-v3",
		})
	})
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
}

func TestHealthCheck_Failure(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if err := client.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected health check to fail")
	}
}

func TestHealthCheck_NotOK(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "loading"})
	})
	err := client.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected health check to fail for non-ok status")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("expected 'not ready' error, got: %v", err)
	}
}

func TestDecideBool_Noul(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		questions := req["questions"].(map[string]interface{})
		q := questions["decision"].(map[string]interface{})
		if q["type"] != "noul" {
			t.Errorf("expected noul type, got %v", q["type"])
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type": "noul",
					"noul": 0.95,
				},
			},
		})
	})

	result, err := client.DecideBool(context.Background(), "state", "question")
	if err != nil {
		t.Fatalf("DecideBool failed: %v", err)
	}
	if !result {
		t.Fatal("expected true for noul=0.95")
	}
}

func TestDecideBool_False(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type": "noul",
					"noul": 0.1,
				},
			},
		})
	})

	result, err := client.DecideBool(context.Background(), "state", "question")
	if err != nil {
		t.Fatalf("DecideBool failed: %v", err)
	}
	if result {
		t.Fatal("expected false for noul=0.1")
	}
}

func TestDecideChoice(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		questions := req["questions"].(map[string]interface{})
		q := questions["decision"].(map[string]interface{})
		if q["type"] != "choice" {
			t.Errorf("expected choice type, got %v", q["type"])
		}
		criteria := q["criteria"].(map[string]interface{})
		if _, ok := criteria["opt1"]; !ok {
			t.Error("expected opt1 in criteria")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":   "choice",
					"choice": "opt2",
				},
			},
		})
	})

	opt, idx, err := client.DecideChoice(context.Background(), "state", "question", "opt1", "opt2", "opt3")
	if err != nil {
		t.Fatalf("DecideChoice failed: %v", err)
	}
	if opt != "opt2" || idx != 1 {
		t.Fatalf("expected opt2/1, got %s/%d", opt, idx)
	}
}

func TestDecide_OrdinalScale(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		questions := req["questions"].(map[string]interface{})
		q := questions["decision"].(map[string]interface{})
		if q["type"] != "score" {
			t.Errorf("expected score type for ordinal scale, got %v", q["type"])
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":  "score",
					"score": 2.0,
				},
			},
		})
	})

	resp, err := client.Decide(context.Background(), jevstyle.DecisionRequest{
		State:    "state",
		Question: "question",
		Options:  []string{"safe", "low risk", "medium risk", "high risk", "critical"},
	})
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}
	if resp.Score != 2.0 {
		t.Fatalf("expected score 2.0, got %f", resp.Score)
	}
	if resp.Option != "medium risk" {
		t.Fatalf("expected 'medium risk', got %q", resp.Option)
	}
}

func TestIsCommandDangerous_True(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		questions := req["questions"].(map[string]interface{})
		q := questions["decision"].(map[string]interface{})
		if q["type"] != "score" {
			t.Errorf("expected score type, got %v", q["type"])
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":  "score",
					"score": 3.5,
				},
			},
		})
	})

	dangerous, err := client.IsCommandDangerous(context.Background(), "rm -rf /")
	if err != nil {
		t.Fatalf("IsCommandDangerous failed: %v", err)
	}
	if !dangerous {
		t.Fatal("expected dangerous for score=3.5")
	}
}

func TestIsCommandDangerous_False(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":  "score",
					"score": 0.5,
				},
			},
		})
	})

	dangerous, err := client.IsCommandDangerous(context.Background(), "ls")
	if err != nil {
		t.Fatalf("IsCommandDangerous failed: %v", err)
	}
	if dangerous {
		t.Fatal("expected safe for score=0.5")
	}
}

func TestCheckGoalCompletion_True(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":  "score",
					"score": 2.5,
				},
			},
		})
	})

	complete, err := client.CheckGoalCompletion(context.Background(), "goal", "progress")
	if err != nil {
		t.Fatalf("CheckGoalCompletion failed: %v", err)
	}
	if !complete {
		t.Fatal("expected complete for score=2.5")
	}
}

func TestCheckGoalCompletion_False(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":  "score",
					"score": 0.5,
				},
			},
		})
	})

	complete, err := client.CheckGoalCompletion(context.Background(), "goal", "progress")
	if err != nil {
		t.Fatalf("CheckGoalCompletion failed: %v", err)
	}
	if complete {
		t.Fatal("expected incomplete for score=0.5")
	}
}

func TestDisambiguatePath(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":   "choice",
					"choice": "path/b",
				},
			},
		})
	})

	chosen, ok, err := client.DisambiguatePath(context.Background(), "hint", []string{"path/a", "path/b", "path/c"})
	if err != nil {
		t.Fatalf("DisambiguatePath failed: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if chosen != "path/b" {
		t.Fatalf("expected path/b, got %q", chosen)
	}
}

func TestDisambiguatePath_SingleCandidate(t *testing.T) {
	client := New("http://localhost:8765")
	chosen, ok, err := client.DisambiguatePath(context.Background(), "hint", []string{"only/path"})
	if err != nil {
		t.Fatalf("DisambiguatePath failed: %v", err)
	}
	if !ok || chosen != "only/path" {
		t.Fatalf("expected only/path, got %q/%v", chosen, ok)
	}
}

func TestClassifyFailure_Confident(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":       "choice",
					"choice":     "Syntax or compile error",
					"confidence": 0.9,
				},
			},
		})
	})

	result, err := client.ClassifyFailure(context.Background(), "syntax error")
	if err != nil {
		t.Fatalf("ClassifyFailure failed: %v", err)
	}
	if result != "Syntax or compile error" {
		t.Fatalf("expected 'Syntax or compile error', got %q", result)
	}
}

func TestClassifyFailure_Uncertain(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":       "choice",
					"choice":     "Syntax or compile error",
					"confidence": 0.3,
				},
			},
		})
	})

	result, err := client.ClassifyFailure(context.Background(), "syntax error")
	if err != nil {
		t.Fatalf("ClassifyFailure failed: %v", err)
	}
	if !strings.Contains(result, "uncertain") {
		t.Fatalf("expected '(uncertain)' suffix, got %q", result)
	}
}

func TestClassifyCommit(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":       "choice",
					"choice":     "feat (new feature or capability)",
					"confidence": 0.85,
				},
			},
		})
	})

	result, err := client.ClassifyCommit(context.Background(), "+func NewFeature()")
	if err != nil {
		t.Fatalf("ClassifyCommit failed: %v", err)
	}
	if result != "feat" {
		t.Fatalf("expected 'feat', got %q", result)
	}
}

func TestServerError(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"code":    "invalid_question",
				"message": "choice criteria must be an object",
			},
		})
	})

	_, err := client.DecideBool(context.Background(), "state", "question")
	if err == nil {
		t.Fatal("expected error for 422 response")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Fatalf("expected 422 in error, got: %v", err)
	}
}

func TestTimeout(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"decision": map[string]interface{}{
					"type": "noul",
					"noul": 0.9,
				},
			},
		})
	})
	client.WithTimeout(10 * time.Millisecond)

	_, err := client.DecideBool(context.Background(), "state", "question")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAskSafetyWithContext_IncludesNote(t *testing.T) {
	var gotState string
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		gotState, _ = req["state"].(string)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"action": map[string]interface{}{"type": "choice", "choice": "allow", "confidence": 0.9},
				"risk":   map[string]interface{}{"type": "score", "score": 0.2},
			},
		})
	})

	note := "The user supplied /tmp/vibe_clipboard_1.png from their own clipboard."
	if _, err := client.AskSafetyWithContext(context.Background(), "bash", "ls -l /tmp/vibe_clipboard_1.png", note); err != nil {
		t.Fatalf("AskSafetyWithContext failed: %v", err)
	}

	if !strings.Contains(gotState, "ls -l /tmp/vibe_clipboard_1.png") {
		t.Errorf("state missing the action detail: %q", gotState)
	}
	if !strings.Contains(gotState, note) {
		t.Errorf("state missing the runtime context note: %q", gotState)
	}
	if !strings.Contains(gotState, "not chosen by the agent") {
		t.Errorf("state should mark the context as not agent-chosen: %q", gotState)
	}
}

func TestAskSafetyWithContext_EmptyNoteKeepsStateClean(t *testing.T) {
	var gotState string
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		gotState, _ = req["state"].(string)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"action": map[string]interface{}{"type": "choice", "choice": "allow", "confidence": 0.9},
				"risk":   map[string]interface{}{"type": "score", "score": 0.2},
			},
		})
	})

	if _, err := client.AskSafetyWithContext(context.Background(), "bash", "ls", "   "); err != nil {
		t.Fatalf("AskSafetyWithContext failed: %v", err)
	}
	if strings.Contains(gotState, "Runtime context") {
		t.Errorf("blank note should not add a context section: %q", gotState)
	}
}

func TestAskSafety_DelegatesToContextVariant(t *testing.T) {
	var gotState string
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		gotState, _ = req["state"].(string)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"action": map[string]interface{}{"type": "choice", "choice": "allow", "confidence": 0.9},
				"risk":   map[string]interface{}{"type": "score", "score": 0.2},
			},
		})
	})

	if _, err := client.AskSafety(context.Background(), "bash", "ls"); err != nil {
		t.Fatalf("AskSafety failed: %v", err)
	}
	if !strings.Contains(gotState, "ls") {
		t.Errorf("AskSafety state missing detail: %q", gotState)
	}
	if strings.Contains(gotState, "Runtime context") {
		t.Errorf("AskSafety must not add context: %q", gotState)
	}
}

func TestAskSafety(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		questions := req["questions"].(map[string]interface{})
		// Verify both questions are present
		if _, ok := questions["action"]; !ok {
			t.Error("missing 'action' question")
		}
		if _, ok := questions["risk"]; !ok {
			t.Error("missing 'risk' question")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"action": map[string]interface{}{
					"type":       "choice",
					"choice":     "allow",
					"confidence": 0.9,
				},
				"risk": map[string]interface{}{
					"type":  "score",
					"score": 0.5,
				},
			},
		})
	})

	decision, err := client.AskSafety(context.Background(), "bash", "rm -rf /tmp/test")
	if err != nil {
		t.Fatalf("AskSafety failed: %v", err)
	}
	if !decision.Allowed() {
		t.Fatal("expected allow")
	}
	if !decision.Confident() {
		t.Fatal("expected confident (0.9 >= 0.70)")
	}
	if decision.RiskScore != 0.5 {
		t.Fatalf("expected risk score 0.5, got %f", decision.RiskScore)
	}
}

func TestAskSafety_LowConfidence(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"action": map[string]interface{}{
					"type":       "choice",
					"choice":     "allow",
					"confidence": 0.5,
				},
				"risk": map[string]interface{}{
					"type":  "score",
					"score": 2.5,
				},
			},
		})
	})

	decision, err := client.AskSafety(context.Background(), "bash", "git push --force")
	if err != nil {
		t.Fatalf("AskSafety failed: %v", err)
	}
	if !decision.Allowed() {
		t.Fatal("expected allow")
	}
	if decision.Confident() {
		t.Fatal("expected not confident (0.5 < 0.70)")
	}
}

func TestAskSafety_Block(t *testing.T) {
	client, _ := newMockV3Server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "test",
			"answers": map[string]interface{}{
				"action": map[string]interface{}{
					"type":       "choice",
					"choice":     "block",
					"confidence": 0.95,
				},
				"risk": map[string]interface{}{
					"type":  "score",
					"score": 3.0,
				},
			},
		})
	})

	decision, err := client.AskSafety(context.Background(), "bash", "DROP TABLE users")
	if err != nil {
		t.Fatalf("AskSafety failed: %v", err)
	}
	if !decision.Blocked() {
		t.Fatal("expected block")
	}
}

func TestAskSafety_NotEnabled(t *testing.T) {
	client := New("")
	_, err := client.AskSafety(context.Background(), "bash", "test")
	if err == nil {
		t.Fatal("expected error for disabled client")
	}
}

func TestIsOrdinalScale(t *testing.T) {
	tests := []struct {
		options []string
		want    bool
	}{
		{[]string{"safe", "low risk", "medium risk", "high risk", "critical"}, true},
		{[]string{"not started", "in progress", "mostly done", "completed"}, true},
		{[]string{"low", "medium", "high"}, true},
		{[]string{"Yes", "No"}, false},
		{[]string{"opt1", "opt2", "opt3"}, false},
		{[]string{}, false},
		{[]string{"only"}, false},
	}
	for _, tt := range tests {
		got := isOrdinalScale(tt.options)
		if got != tt.want {
			t.Errorf("isOrdinalScale(%v) = %v, want %v", tt.options, got, tt.want)
		}
	}
}
