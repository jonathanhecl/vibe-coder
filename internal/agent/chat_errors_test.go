package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestIsContextOverflowError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("Context window exceeded"), true},
		{errors.New("maximum context length is 4096 tokens"), true},
		{errors.New("context_length_exceeded"), true},
		{errors.New("prompt is too long"), true},
		{errors.New("exceeds the available context size"), true},
		{errors.New("http 400: invalid request"), false},
		{errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		if got := isContextOverflowError(tc.err); got != tc.want {
			t.Fatalf("isContextOverflowError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestIsNonRetryableChatError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("http 400: invalid request"), true},
		{errors.New("http 401: unauthorized"), true},
		{errors.New("http 404: model missing"), true},
		{errors.New("http 408: request timeout"), false},
		{errors.New("http 429: too many requests"), false},
		{errors.New("http 500: internal error"), false},
		{errors.New("openai chat failed (400 Bad Request): bad input"), true},
		{errors.New("openai chat failed (429 Too Many Requests): slow down"), false},
		// Overflow errors are handled by the compaction path, never here.
		{errors.New("Context window exceeded"), false},
		{errors.New("http 400: context window exceeded"), false},
		{errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		if got := isNonRetryableChatError(tc.err); got != tc.want {
			t.Fatalf("isNonRetryableChatError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// alwaysChatErrClient fails every Chat call with the same error, counting
// attempts so tests can assert the retry policy.
type alwaysChatErrClient struct {
	fakeClient
	err   error
	calls int32
}

func (c *alwaysChatErrClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	atomic.AddInt32(&c.calls, 1)
	return nil, c.err
}

// overflowThenReplyClient fails the first Chat call with a context-overflow
// error, then streams a normal reply so tests can assert the recovery path
// made exactly one extra attempt.
type overflowThenReplyClient struct {
	fakeClient
	calls int32
}

func (c *overflowThenReplyClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	n := atomic.AddInt32(&c.calls, 1)
	if n == 1 {
		return nil, errors.New("Context window exceeded")
	}
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: "recovered answer", Done: true}
	close(ch)
	return ch, nil
}

// overflowThenStreamErrClient fails the first Chat call with a stream chunk
// carrying a context-overflow error, then streams a normal reply. It covers
// the streamAssistantResponse error path, not just the Chat dial path.
type overflowThenStreamErrClient struct {
	fakeClient
	calls int32
}

func (c *overflowThenStreamErrClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	n := atomic.AddInt32(&c.calls, 1)
	ch := make(chan ollama.Chunk, 2)
	if n == 1 {
		ch <- ollama.Chunk{Err: errors.New("Context window exceeded")}
	} else {
		ch <- ollama.Chunk{Delta: "recovered answer", Done: true}
	}
	close(ch)
	return ch, nil
}

// newChatErrAgent builds an agent whose session is pre-filled with enough
// history that CompactNow actually rewrites the transcript (generation
// advances) instead of no-oping on a tiny conversation.
func newChatErrAgent(t *testing.T, client ollama.Client, fillHistory bool) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "test-model",
		ContextWindow: 4096,
		MaxTokens:     128,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	sess := session.New(cfg)
	if fillHistory {
		for i := 0; i < 30; i++ {
			sess.AddUser(strings.Repeat("filler-context-", 30))
		}
	}
	return New(cfg, client, reg, perm, sess, &fakeUI{})
}

// A first-attempt context overflow compacts the conversation and retries the
// same chatOnce call immediately: the turn survives with exactly two Chat
// calls and no retry sleeps.
func TestChatOnceContextOverflowCompactsAndRetries(t *testing.T) {
	client := &overflowThenReplyClient{}
	ag := newChatErrAgent(t, client, true)

	reply, _, err := ag.chatOnce(context.Background())
	if err != nil {
		t.Fatalf("expected recovery after compaction, got %v", err)
	}
	if !strings.Contains(reply, "recovered answer") {
		t.Fatalf("unexpected reply %q", reply)
	}
	if calls := atomic.LoadInt32(&client.calls); calls != 2 {
		t.Fatalf("expected exactly 2 Chat calls, got %d", calls)
	}
}

// The same recovery must work when the overflow arrives mid-stream instead
// of as a Chat dial error.
func TestChatOnceStreamContextOverflowCompactsAndRetries(t *testing.T) {
	client := &overflowThenStreamErrClient{}
	ag := newChatErrAgent(t, client, true)

	reply, _, err := ag.chatOnce(context.Background())
	if err != nil {
		t.Fatalf("expected recovery after compaction, got %v", err)
	}
	if !strings.Contains(reply, "recovered answer") {
		t.Fatalf("unexpected reply %q", reply)
	}
	if calls := atomic.LoadInt32(&client.calls); calls != 2 {
		t.Fatalf("expected exactly 2 Chat calls, got %d", calls)
	}
}

// When the request still does not fit after compaction, chatOnce returns the
// terminal ErrContextOverflow after exactly one compaction + one retry —
// never burning the ordinary retry sleeps.
func TestChatOncePersistentContextOverflowStops(t *testing.T) {
	client := &alwaysChatErrClient{err: errors.New("Context window exceeded")}
	ag := newChatErrAgent(t, client, true)

	start := time.Now()
	_, _, err := ag.chatOnce(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected context overflow error")
	}
	if !IsContextOverflowErr(err) {
		t.Fatalf("expected IsContextOverflowErr, got %v", err)
	}
	if !strings.Contains(err.Error(), "--max-tokens") || !strings.Contains(err.Error(), "--context-window") {
		t.Fatalf("expected actionable flags in error, got %v", err)
	}
	if calls := atomic.LoadInt32(&client.calls); calls != 2 {
		t.Fatalf("expected exactly 2 Chat calls (initial + post-compaction), got %d", calls)
	}
	if elapsed >= time.Second {
		t.Fatalf("expected no retry sleeps, took %v", elapsed)
	}
}

// A non-retryable 4xx returns immediately: one Chat call, no sleeps, no
// sidecar fallback.
func TestChatOnceNonRetryable4xxReturnsImmediately(t *testing.T) {
	client := &alwaysChatErrClient{err: errors.New("http 400: invalid request")}
	ag := newChatErrAgent(t, client, false)

	_, _, err := ag.chatOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("expected the 400 error, got %v", err)
	}
	if calls := atomic.LoadInt32(&client.calls); calls != 1 {
		t.Fatalf("expected exactly 1 Chat call, got %d", calls)
	}
}

// 429 and 500 stay retryable: they exhaust the normal attempt budget.
func TestChatOnceRetryableHTTPStatusesStillRetry(t *testing.T) {
	for _, statusErr := range []string{"http 429: too many requests", "http 500: internal error"} {
		statusErr := statusErr
		t.Run(statusErr, func(t *testing.T) {
			client := &alwaysChatErrClient{err: errors.New(statusErr)}
			ag := newChatErrAgent(t, client, false)

			_, _, err := ag.chatOnce(context.Background())
			if err == nil {
				t.Fatal("expected the HTTP error to be returned")
			}
			if calls := atomic.LoadInt32(&client.calls); calls != MaxRetries+1 {
				t.Fatalf("expected %d Chat calls for %s, got %d", MaxRetries+1, statusErr, calls)
			}
		})
	}
}
