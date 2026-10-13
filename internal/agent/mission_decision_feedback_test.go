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

func newMissionFeedbackAgent(t *testing.T, decisionModel string) (*Agent, *session.Session) {
	t.Helper()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		Model:         "main-model",
		DecisionModel: decisionModel,
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
	ag, sess := newMissionFeedbackAgent(t, "decision-model")
	ag.mission.Start("Build the authentication system")
	ag.SetDecider(&stubDecider{enabled: true, model: "decision-model", goalDoneResult: false})

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done || !reviewed {
		t.Fatalf("expected done=false reviewed=true, got done=%t reviewed=%t", done, reviewed)
	}
	if !hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("expected an explicit decision-model incomplete reminder to be injected into the session")
	}
}

func TestReviewMissionCompletionNoFeedbackWhenComplete(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "decision-model")
	ag.mission.Start("Build the authentication system")
	ag.SetDecider(&stubDecider{enabled: true, model: "decision-model", goalDoneResult: true})

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done || !reviewed {
		t.Fatalf("expected done=true reviewed=true, got done=%t reviewed=%t", done, reviewed)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("did not expect an incomplete reminder when the decision model confirms completion")
	}
}

func TestReviewMissionCompletionWithoutDeciderIsReadOnly(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "")
	ag.mission.Start("Build the authentication system")

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done || reviewed {
		t.Fatalf("expected done=false reviewed=false without a decision model, got done=%t reviewed=%t", done, reviewed)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("did not expect feedback when no decision model is configured")
	}
}

func TestReviewMissionCompletionErrorDoesNotInject(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "decision-model")
	ag.mission.Start("Build the authentication system")
	ag.SetDecider(&stubDecider{enabled: true, model: "decision-model", goalDoneErr: errors.New("boom")})

	done, reviewed, err := ag.ReviewMissionCompletion(context.Background())
	if err == nil {
		t.Fatal("expected error from the decision model to propagate")
	}
	if done || !reviewed {
		t.Fatalf("expected done=false reviewed=true on error, got done=%t reviewed=%t", done, reviewed)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("did not expect feedback when the decision call fails")
	}
}

func TestCheckMissionCompletionDoesNotInjectFeedback(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "decision-model")
	ag.mission.Start("Build the authentication system")
	ag.SetDecider(&stubDecider{enabled: true, model: "decision-model", goalDoneResult: false})

	done, err := ag.CheckMissionCompletion(context.Background())
	if err != nil || done {
		t.Fatalf("expected done=false err=nil, got done=%t err=%v", done, err)
	}
	if hasRuntimeReminder(sess, "not yet complete") {
		t.Fatal("CheckMissionCompletion must stay read-only")
	}
}
