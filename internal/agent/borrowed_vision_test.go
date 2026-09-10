package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

func newBorrowedVisionAgent(t *testing.T, mainKnown, mainAvailable, sideKnown, sideAvailable bool) (*Agent, *stubClient) {
	t.Helper()
	sc := &stubClient{reply: "A blue jacket with red buttons."}
	sideModel := "moondream"
	if !sideKnown && !sideAvailable {
		sideModel = ""
	}
	a, _ := newTestAgent(t, sideModel, sc)
	a.SetSidecar(sidecar.New(a.cfg, sc))
	a.cfg.Model = "qwen3.5:9b"
	a.cfg.VisionKnown = mainKnown
	a.cfg.VisionAvailable = mainAvailable
	a.cfg.SidecarVisionKnown = sideKnown
	a.cfg.SidecarVisionAvailable = sideAvailable
	return a, sc
}

func TestBorrowedVisionSubstitutesDescription(t *testing.T) {
	t.Parallel()
	a, sc := newBorrowedVisionAgent(t, true, false, true, true)
	photo := filepath.Join(t.TempDir(), "jacket.png")
	writeTestPNG(t, photo)
	a.sess.AddToolObservation("Read", vision.MarkerFor(photo))

	msgs := a.buildOllamaMessages(context.Background(), "SYS")
	last := msgs[len(msgs)-1]
	if len(last.Images) != 0 {
		t.Fatalf("expected no raw images for a blind main model, got %d", len(last.Images))
	}
	if !strings.Contains(last.Content, "[sidecar-vision file=jacket.png]") {
		t.Fatalf("expected sidecar-vision block, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "A blue jacket with red buttons.") {
		t.Fatalf("expected sidecar description, got %q", last.Content)
	}

	// Second build reuses the cached description: no new sidecar call.
	_ = a.buildOllamaMessages(context.Background(), "SYS")
	if got := atomic.LoadInt32(&sc.calls); got != 1 {
		t.Fatalf("expected 1 sidecar call, got %d", got)
	}
}

func TestBorrowedVisionFailureIsHonest(t *testing.T) {
	t.Parallel()
	a, _ := newBorrowedVisionAgent(t, true, false, true, true)
	a.sess.AddToolObservation("Read", vision.MarkerFor(filepath.Join(t.TempDir(), "gone.png")))
	msgs := a.buildOllamaMessages(context.Background(), "SYS")
	if !strings.Contains(msgs[len(msgs)-1].Content, "(image unavailable:") {
		t.Fatalf("expected unavailable note, got %q", msgs[len(msgs)-1].Content)
	}
}

func TestNoVisionAnywhereMarksUnavailable(t *testing.T) {
	t.Parallel()
	a, sc := newBorrowedVisionAgent(t, true, false, false, false)
	photo := filepath.Join(t.TempDir(), "jacket.png")
	writeTestPNG(t, photo)
	a.sess.AddToolObservation("Read", vision.MarkerFor(photo))
	msgs := a.buildOllamaMessages(context.Background(), "SYS")
	got := msgs[len(msgs)-1].Content
	if !strings.Contains(got, "(image unavailable: neither") || !strings.Contains(got, "/model") {
		t.Fatalf("expected honest unavailable note, got %q", got)
	}
	if len(msgs[len(msgs)-1].Images) != 0 {
		t.Fatal("expected no images attached")
	}
	if got := atomic.LoadInt32(&sc.calls); got != 0 {
		t.Fatalf("expected no sidecar calls, got %d", got)
	}
}

func TestMainVisionBeatsSidecar(t *testing.T) {
	t.Parallel()
	a, sc := newBorrowedVisionAgent(t, true, true, true, true)
	photo := filepath.Join(t.TempDir(), "jacket.png")
	writeTestPNG(t, photo)
	a.sess.AddToolObservation("Read", vision.MarkerFor(photo))
	msgs := a.buildOllamaMessages(context.Background(), "SYS")
	if len(msgs[len(msgs)-1].Images) != 1 {
		t.Fatal("expected direct attach when main can see")
	}
	if got := atomic.LoadInt32(&sc.calls); got != 0 {
		t.Fatalf("expected no sidecar calls, got %d", got)
	}
}

func TestUnknownMainTriesDirectAttach(t *testing.T) {
	t.Parallel()
	a, sc := newBorrowedVisionAgent(t, false, false, true, true)
	photo := filepath.Join(t.TempDir(), "jacket.png")
	writeTestPNG(t, photo)
	a.sess.AddToolObservation("Read", vision.MarkerFor(photo))
	msgs := a.buildOllamaMessages(context.Background(), "SYS")
	if len(msgs[len(msgs)-1].Images) != 1 {
		t.Fatal("expected honest direct attempt on unknown capability")
	}
	if got := atomic.LoadInt32(&sc.calls); got != 0 {
		t.Fatalf("expected no sidecar calls, got %d", got)
	}
}
