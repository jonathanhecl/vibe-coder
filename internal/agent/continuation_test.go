package agent

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestIsContinuationMessage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"continua", true},
		{"continúa", true},
		{"Continue.", true},
		{"  go on  ", true},
		{"please implement feature X", false},
	}
	for _, tc := range cases {
		if got := isContinuationMessage(tc.in); got != tc.want {
			t.Fatalf("isContinuationMessage(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestResolveGoalForRunContinuesPriorAsk(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := session.New(cfg)
	s.AddUser("Make the hide character print a welcome line.")
	if g := resolveGoalForRun(s, "continua"); g != "Make the hide character print a welcome line." {
		t.Fatalf("unexpected goal: %q", g)
	}
	s.AddUser("continua")
	s.AddUser("continua")
	if g := resolveGoalForRun(s, "continue"); g != "Make the hide character print a welcome line." {
		t.Fatalf("unexpected goal after follow-ups: %q", g)
	}
}

func TestBuildOllamaMessagesIncludesTranscript(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Model:         "m",
		ContextWindow: 8000,
		MaxTokens:     32,
		Temperature:   0.1,
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
	}
	sess := session.New(cfg)
	sess.AddUser("first instruction")
	sess.AddAssistant("partial reply")
	reg := tools.NewRegistry()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ag := New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})
	msgs := ag.buildOllamaMessages("SYSTEM_PROMPT")
	if len(msgs) != 3 {
		t.Fatalf("expected system + user + assistant, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "SYSTEM_PROMPT" {
		t.Fatalf("unexpected first message: %#v", msgs[0])
	}
	if msgs[1].Content != "first instruction" || msgs[2].Content != "partial reply" {
		t.Fatalf("unexpected transcript order: %#v", msgs)
	}
}
