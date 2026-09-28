package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newMissionFeedbackAgent(t *testing.T, jevModel string) (*Agent, *session.Session) {
	t.Helper()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		Model:         "main-model",
		JevstyleModel: jevModel,
	}
	sess := session.New(cfg)
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), sess, &fakeUI{})
	return ag, sess
}

func hasRuntimeReminder(sess *session.Session, needle string) bool {
	for _, m := range sess.MessagesReadOnly() {
		if strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}

func TestReviewMissionCompletionInjectsFeedbackWhenIncomplete(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "jev-model")
	ag.mission.Start("Build the authentication system")
	ag.SetJevstyle(&stubJevDecider{enabled: true, model: "jev-model", goalDoneResult: false})

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done || !reviewed {
		t.Fatalf("expected done=false reviewed=true, got done=%t reviewed=%t", done, reviewed)
	}
	if !hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("expected an explicit JEV incomplete reminder to be injected into the session")
	}
}

func TestReviewMissionCompletionNoFeedbackWhenComplete(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "jev-model")
	ag.mission.Start("Build the authentication system")
	ag.SetJevstyle(&stubJevDecider{enabled: true, model: "jev-model", goalDoneResult: true})

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done || !reviewed {
		t.Fatalf("expected done=true reviewed=true, got done=%t reviewed=%t", done, reviewed)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("did not expect an incomplete reminder when JEV confirms completion")
	}
}

func TestReviewMissionCompletionWithoutJevIsReadOnly(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "")
	ag.mission.Start("Build the authentication system")

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done || reviewed {
		t.Fatalf("expected done=false reviewed=false without JEV, got done=%t reviewed=%t", done, reviewed)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("did not expect feedback when JEV is not configured")
	}
}

func TestReviewMissionCompletionErrorDoesNotInject(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "jev-model")
	ag.mission.Start("Build the authentication system")
	ag.SetJevstyle(&stubJevDecider{enabled: true, model: "jev-model", goalDoneErr: errors.New("boom")})

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err == nil {
		t.Fatal("expected error from JEV to propagate")
	}
	if done || !reviewed {
		t.Fatalf("expected done=false reviewed=true on error, got done=%t reviewed=%t", done, reviewed)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("did not expect feedback when the JEV call fails")
	}
}

func TestCheckMissionCompletionDoesNotInjectFeedback(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "jev-model")
	ag.mission.Start("Build the authentication system")
	ag.SetJevstyle(&stubJevDecider{enabled: true, model: "jev-model", goalDoneResult: false})

	done, err := ag.CheckMissionCompletion(context.Background())
	if err != nil || done {
		t.Fatalf("expected done=false err=nil, got done=%t err=%v", done, err)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("CheckMissionCompletion must stay read-only")
	}
}
