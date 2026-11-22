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

type recordingMissionDecider struct {
	stubDecider
	evidence []string
}

func (r *recordingMissionDecider) CheckGoalCompletion(ctx context.Context, goal, recentProgress string) (bool, error) {
	r.evidence = append(r.evidence, recentProgress)
	return r.stubDecider.CheckGoalCompletion(ctx, goal, recentProgress)
}

func TestMissionCompletionReviewerSkipsUnchangedEvidence(t *testing.T) {
	ag, sess := newMissionFeedbackAgent(t, "decision-model")
	ag.mission.Start("Build the authentication system")
	rec := &recordingMissionDecider{stubDecider: stubDecider{enabled: true, model: "decision-model", goalDoneResult: false}}
	ag.SetDecider(rec)

	review := ag.NewMissionCompletionReviewer()
	ctx := context.Background()

	sess.AddToolObservation("Bash", "CHECK PASSED")
	done, reviewed, err := review(ctx)
	if err != nil || done || !reviewed {
		t.Fatalf("expected done=false reviewed=true err=nil, got done=%t reviewed=%t err=%v", done, reviewed, err)
	}
	if len(rec.evidence) != 1 || !strings.Contains(rec.evidence[0], "CHECK PASSED") {
		t.Fatalf("expected one consultation whose evidence contains the check output, got %v", rec.evidence)
	}

	// Repeating the same observation dedupes in the summary and a plain user
	// message (including the injected incomplete reminder) is not evidence:
	// the verdict must not be requested again while nothing changed.
	sess.AddToolObservation("Bash", "CHECK PASSED")
	sess.AddToolResult("Bash", "CHECK PASSED")
	sess.AddUser("Plain follow-up is not completion evidence")
	done, reviewed, err = review(ctx)
	if err != nil || done || reviewed {
		t.Fatalf("expected done=false reviewed=false err=nil on unchanged evidence, got done=%t reviewed=%t err=%v", done, reviewed, err)
	}
	if len(rec.evidence) != 1 {
		t.Fatalf("expected still one consultation, got %d", len(rec.evidence))
	}

	sess.AddToolResult("Bash", "NEW CHECK PASSED")
	done, reviewed, err = review(ctx)
	if err != nil || done || !reviewed {
		t.Fatalf("expected done=false reviewed=true err=nil on new evidence, got done=%t reviewed=%t err=%v", done, reviewed, err)
	}
	if len(rec.evidence) != 2 {
		t.Fatalf("expected two consultations, got %d", len(rec.evidence))
	}
	if !strings.Contains(rec.evidence[1], "CHECK PASSED") || !strings.Contains(rec.evidence[1], "NEW CHECK PASSED") {
		t.Fatalf("expected second evidence to include both observations, got %q", rec.evidence[1])
	}

	// A fresh reviewer (manual resume) consults again even though a previous
	// reviewer already saw this evidence.
	resumed := ag.NewMissionCompletionReviewer()
	done, reviewed, err = resumed(ctx)
	if err != nil || done || !reviewed {
		t.Fatalf("expected a new reviewer to re-consult, got done=%t reviewed=%t err=%v", done, reviewed, err)
	}
	if len(rec.evidence) != 3 {
		t.Fatalf("expected three consultations after resume, got %d", len(rec.evidence))
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
