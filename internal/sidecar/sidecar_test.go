package sidecar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// fakeClient is the minimum subset of ollama.Client we need: only ChatSync
// is exercised by Pool. All other methods return errors so an accidental
// regression that calls them would fail loudly.
type fakeClient struct {
	mu       sync.Mutex
	calls    int32
	delay    time.Duration
	reply    string
	replyFn  func(req ollama.ChatRequest) (string, error)
	lastReqs []ollama.ChatRequest
}

func (f *fakeClient) Chat(ctx context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	return nil, errors.New("not used")
}

func (f *fakeClient) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	f.lastReqs = append(f.lastReqs, req)
	delay := f.delay
	fn := f.replyFn
	reply := f.reply
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ollama.ChatResponse{}, ctx.Err()
		}
	}
	if fn != nil {
		c, err := fn(req)
		return ollama.ChatResponse{Content: c}, err
	}
	return ollama.ChatResponse{Content: reply}, nil
}

func (f *fakeClient) Tags(ctx context.Context) ([]ollama.Model, error) {
	return nil, errors.New("not used")
}
func (f *fakeClient) Version(ctx context.Context) (string, error) { return "", errors.New("not used") }
func (f *fakeClient) Pull(ctx context.Context, model string, p func(ollama.PullEvent)) error {
	return errors.New("not used")
}

func TestEnabledRequiresClientAndModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		client          ollama.Client
		model           string
		sidecarDisabled bool
		skipSession     bool
		want            bool
	}{
		{"all set", &fakeClient{}, "qwen3.5:4b", false, false, true},
		{"disabled in config", &fakeClient{}, "qwen3.5:4b", true, false, false},
		{"session skip", &fakeClient{}, "qwen3.5:4b", false, true, false},
		{"empty model", &fakeClient{}, "", false, false, false},
		{"whitespace model", &fakeClient{}, "   ", false, false, false},
		{"nil client", nil, "qwen3.5:4b", false, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := New(&config.Config{
				SidecarModel:       tc.model,
				SidecarDisabled:    tc.sidecarDisabled,
				SidecarSkipSession: tc.skipSession,
			}, tc.client)
			if got := p.Enabled(); got != tc.want {
				t.Fatalf("Enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSummariseShortOutputIsSkipped(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{reply: "should not be called"}
	p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc, WithSummariseThreshold(1024))
	out, used, err := p.SummariseToolOutput(context.Background(), "Read", "small body")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used || out != "" {
		t.Fatalf("expected no summary for short output, got used=%v out=%q", used, out)
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatalf("sidecar was called for short output")
	}
}

func TestSummariseDisabledShortCircuits(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{reply: "irrelevant"}
	p := New(&config.Config{SidecarModel: ""}, fc)
	out, used, err := p.SummariseToolOutput(context.Background(), "Read", strings.Repeat("x", 10_000))
	if err != nil || used || out != "" {
		t.Fatalf("disabled pool should be inert, got out=%q used=%v err=%v", out, used, err)
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatalf("disabled pool must not call the client")
	}
}

func TestSummariseWrapsAndCaches(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{reply: "- file foo.go\n- func Bar()"}
	p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc, WithSummariseThreshold(10))

	long := strings.Repeat("payload\n", 500)
	out, used, err := p.SummariseToolOutput(context.Background(), "Read", long)
	if err != nil || !used {
		t.Fatalf("expected summary, got used=%v err=%v", used, err)
	}
	if !strings.Contains(out, "[sidecar-summary tool=Read") {
		t.Fatalf("missing envelope: %q", out)
	}
	if !strings.Contains(out, "func Bar()") {
		t.Fatalf("missing summary body: %q", out)
	}

	out2, used2, _ := p.SummariseToolOutput(context.Background(), "Read", long)
	if !used2 || out2 != out {
		t.Fatalf("expected cached identical result, got %q", out2)
	}
	if atomic.LoadInt32(&fc.calls) != 1 {
		t.Fatalf("expected 1 client call thanks to cache, got %d", fc.calls)
	}
}

func TestSummariseSingleflightDeduplicates(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{
		delay: 80 * time.Millisecond,
		reply: "- ok",
	}
	p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc, WithSummariseThreshold(10))
	long := strings.Repeat("x", 4096)

	const N = 6
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _, _ = p.SummariseToolOutput(context.Background(), "Read", long)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&fc.calls); got != 1 {
		t.Fatalf("singleflight should collapse to 1 call, got %d", got)
	}
}

func TestSummariseRespectsContextCancel(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{delay: 5 * time.Second, reply: "late"}
	p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc, WithSummariseThreshold(10))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, used, err := p.SummariseToolOutput(ctx, "Read", strings.Repeat("y", 4096))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Fatalf("cancelled context should not produce a summary")
	}
}

func TestDisambiguatePathSinglyTrivial(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{reply: "PICK: 1"}
	p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc)

	got, ok, err := p.DisambiguatePath(context.Background(), "config", []string{"/abs/only.go"})
	if err != nil || !ok || got != "/abs/only.go" {
		t.Fatalf("single candidate should short-circuit, got %q ok=%v err=%v", got, ok, err)
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatalf("no client call expected for single candidate")
	}
}

func TestDisambiguatePathPicksByNumber(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{reply: "PICK: 2"}
	p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc)

	cands := []string{"/a/config.go", "/b/config.go", "/c/config.go"}
	got, ok, err := p.DisambiguatePath(context.Background(), "user mentioned 'b config'", cands)
	if err != nil || !ok || got != "/b/config.go" {
		t.Fatalf("expected pick #2, got %q ok=%v err=%v", got, ok, err)
	}
}

func TestDisambiguatePathRejectsBogusReply(t *testing.T) {
	t.Parallel()

	cases := []string{
		"i don't know",
		"PICK: 99",
		"PICK: 0",
		"PICK: -1",
		"",
	}
	for _, reply := range cases {
		reply := reply
		t.Run(reply, func(t *testing.T) {
			t.Parallel()
			fc := &fakeClient{reply: reply}
			p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc)
			got, ok, _ := p.DisambiguatePath(context.Background(), "x", []string{"/a", "/b"})
			if ok || got != "" {
				t.Fatalf("expected decline for reply %q, got %q ok=%v", reply, got, ok)
			}
		})
	}
}

func TestPoolNoOpWhenSidecarErrors(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{
		replyFn: func(req ollama.ChatRequest) (string, error) {
			return "", errors.New("sidecar exploded")
		},
	}
	p := New(&config.Config{SidecarModel: "qwen3.5:4b"}, fc, WithSummariseThreshold(10))
	out, used, err := p.SummariseToolOutput(context.Background(), "Read", strings.Repeat("z", 4096))
	if err != nil {
		t.Fatalf("error must stay internal, got %v", err)
	}
	if used || out != "" {
		t.Fatalf("failed sidecar should not produce a summary")
	}
}

func TestParsePickHandlesNoise(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"PICK: 3":                 3,
		"sure, PICK: 2 obviously": 2,
		"pick: 1":                 1,
		"PICK:5":                  5,
		"none of them":            0,
		"PICK: ten":               0,
	}
	for in, want := range cases {
		if got := parsePick(in, 5); got != want {
			t.Fatalf("parsePick(%q, 5) = %d, want %d", in, got, want)
		}
	}
}

func TestClipToolOutputForSidecarTruncatesGlobLines(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := range 900 {
		fmt.Fprintf(&b, "path-%d\n", i)
	}
	out := clipToolOutputForSidecar("Glob", b.String())
	if !strings.Contains(out, "omitted") {
		t.Fatalf("expected line-based truncation marker, got len %d", len(out))
	}
	if strings.Count(out, "\n") > maxListToolLines+4 {
		t.Fatalf("expected roughly %d lines, got %d", maxListToolLines, strings.Count(out, "\n"))
	}
}
