package visionrole

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

type fakeClient struct {
	calls    int32
	reply    string
	thinking string
	err      error
	last     ollama.ChatRequest
}

func (f *fakeClient) Chat(ctx context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	return nil, errors.New("not used")
}

func (f *fakeClient) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	f.last = req
	if f.err != nil {
		return ollama.ChatResponse{}, f.err
	}
	return ollama.ChatResponse{Content: f.reply, Thinking: f.thinking}, nil
}

func (f *fakeClient) Tags(ctx context.Context) ([]ollama.Model, error) {
	return nil, errors.New("not used")
}

func (f *fakeClient) Version(ctx context.Context) (string, error) {
	return "", errors.New("not used")
}

func (f *fakeClient) Pull(ctx context.Context, model string, p func(ollama.PullEvent)) error {
	return errors.New("not used")
}

func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 16), uint8(y * 16), 80, 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func TestNewIsNilUnlessBound(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	if got := New(nil, "llava:latest"); got != nil {
		t.Fatalf("expected nil without a client, got %+v", got)
	}
	if got := New(fc, ""); got != nil {
		t.Fatalf("expected nil without a model, got %+v", got)
	}
	if got := New(fc, "   "); got != nil {
		t.Fatalf("expected nil for a blank model, got %+v", got)
	}
	// A nil *Describer stays safe to use.
	var d *Describer
	if d.Enabled() {
		t.Fatal("nil describer must not report enabled")
	}
	if d.Model() != "" {
		t.Fatalf("nil describer model must be empty, got %q", d.Model())
	}
	if _, err := d.Describe(context.Background(), "x.png", ""); err == nil {
		t.Fatal("nil describer must fail to describe")
	}
}

func TestDescribeUsesBoundModelAndCaches(t *testing.T) {
	t.Parallel()
	photo := filepath.Join(t.TempDir(), "photo.png")
	writePNG(t, photo)
	fc := &fakeClient{reply: "  A red bicycle against a wall.  "}
	d := New(fc, "llava:latest")
	if !d.Enabled() || d.Model() != "llava:latest" {
		t.Fatalf("unexpected describer state: enabled=%v model=%q", d.Enabled(), d.Model())
	}

	got, err := d.Describe(context.Background(), photo, "What is it?")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if got != "A red bicycle against a wall." {
		t.Fatalf("expected trimmed answer, got %q", got)
	}
	if atomic.LoadInt32(&fc.calls) != 1 {
		t.Fatalf("expected 1 vision call, got %d", fc.calls)
	}
	if fc.last.Model != "llava:latest" {
		t.Fatalf("expected the bound model, got %q", fc.last.Model)
	}
	if len(fc.last.Messages) != 2 || len(fc.last.Messages[1].Images) != 1 {
		t.Fatalf("expected one encoded image, got %+v", fc.last.Messages)
	}
	if fc.last.Messages[0].Content == "" {
		t.Fatal("expected a system prompt for the vision role")
	}
	// Thinking must be off, or reasoning tokens eat the NumPredict budget.
	if fc.last.Think == nil || fc.last.Think.IsActive() {
		t.Fatalf("expected thinking disabled, got %+v", fc.last.Think)
	}

	// Second look reuses the cache: no new call.
	if _, err := d.Describe(context.Background(), photo, "What is it?"); err != nil {
		t.Fatalf("second describe: %v", err)
	}
	if atomic.LoadInt32(&fc.calls) != 1 {
		t.Fatalf("expected the cached description, got %d calls", fc.calls)
	}
}

func TestDescribeDefaultsTheQuestion(t *testing.T) {
	t.Parallel()
	photo := filepath.Join(t.TempDir(), "photo.png")
	writePNG(t, photo)
	fc := &fakeClient{reply: "A cat."}
	d := New(fc, "moondream")
	if _, err := d.Describe(context.Background(), photo, "  "); err != nil {
		t.Fatalf("describe: %v", err)
	}
	if !strings.Contains(fc.last.Messages[1].Content, "Describe this image in thorough detail") {
		t.Fatalf("expected the default question, got %q", fc.last.Messages[1].Content)
	}
}

func TestDescribeRejectsBadInput(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{reply: "x"}
	d := New(fc, "moondream")

	if _, err := d.Describe(context.Background(), "  ", ""); err == nil || !strings.Contains(err.Error(), "image path is empty") {
		t.Fatalf("expected an empty-path error, got %v", err)
	}
	notes := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notes, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Describe(context.Background(), notes, ""); err == nil || !strings.Contains(err.Error(), "unsupported image format") {
		t.Fatalf("expected an unsupported-format error, got %v", err)
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatalf("expected no calls for bad input, got %d", fc.calls)
	}
}

func TestDescribeReportsFailures(t *testing.T) {
	t.Parallel()
	photo := filepath.Join(t.TempDir(), "photo.png")
	writePNG(t, photo)

	empty := &fakeClient{reply: "   "}
	if _, err := New(empty, "moondream").Describe(context.Background(), photo, ""); err == nil || !strings.Contains(err.Error(), "empty description") {
		t.Fatalf("expected an empty-description error, got %v", err)
	}

	broken := &fakeClient{err: errors.New("connection refused")}
	if _, err := New(broken, "moondream").Describe(context.Background(), photo, ""); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected the client error, got %v", err)
	}
}

func TestDescribeRecoversReasoningOnlyReply(t *testing.T) {
	t.Parallel()
	photo := filepath.Join(t.TempDir(), "photo.png")
	writePNG(t, photo)
	// OpenAI-compatible servers ignore think=false and may leave Content empty
	// with the observations in the reasoning text, or wrap them in a think
	// block; the description must still come back.
	cases := []struct {
		name     string
		reply    string
		thinking string
		want     string
	}{
		{name: "empty content falls back to reasoning", reply: "", thinking: "A gradient square, blue to green.", want: "A gradient square, blue to green."},
		{name: "think block is stripped", reply: "<think>unfinished", thinking: "A small colorful tile.", want: "A small colorful tile."},
		{name: "closed think block is stripped from content", reply: "<think>hmm</think>A red car.", want: "A red car."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{reply: tc.reply, thinking: tc.thinking}
			got, err := New(fc, "moondream").Describe(context.Background(), photo, "")
			if err != nil {
				t.Fatalf("describe: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
