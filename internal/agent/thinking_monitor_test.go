package agent

import (
	"context"
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

func TestExtractLastParagraphs(t *testing.T) {
	// Empty text
	if got := extractLastParagraphs("", 5); got != "" {
		t.Fatalf("expected empty for empty text, got %q", got)
	}
	if got := extractLastParagraphs("   \n\n  ", 5); got != "" {
		t.Fatalf("expected empty for whitespace text, got %q", got)
	}

	// Fewer than 5 paragraphs
	threeParas := "Para 1\n\nPara 2\n\nPara 3"
	if got := extractLastParagraphs(threeParas, 5); got != threeParas {
		t.Fatalf("expected %q, got %q", threeParas, got)
	}

	// Exactly 5 paragraphs
	fiveParas := "P1\n\nP2\n\nP3\n\nP4\n\nP5"
	if got := extractLastParagraphs(fiveParas, 5); got != fiveParas {
		t.Fatalf("expected %q, got %q", fiveParas, got)
	}

	// More than 5 paragraphs (e.g. 7 paragraphs)
	sevenParas := "P1\n\nP2\n\nP3\n\nP4\n\nP5\n\nP6\n\nP7"
	expectedLast5 := "P3\n\nP4\n\nP5\n\nP6\n\nP7"
	if got := extractLastParagraphs(sevenParas, 5); got != expectedLast5 {
		t.Fatalf("expected %q, got %q", expectedLast5, got)
	}

	// Single newlines fallback when no double newlines exist
	singleLineSeven := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7"
	expectedLines5 := "Line 3\n\nLine 4\n\nLine 5\n\nLine 6\n\nLine 7"
	if got := extractLastParagraphs(singleLineSeven, 5); got != expectedLines5 {
		t.Fatalf("expected %q, got %q", expectedLines5, got)
	}
}

func TestThinkingLoopDetectionStream(t *testing.T) {
	origInterval := thinkingCheckInterval
	thinkingCheckInterval = 20 * time.Millisecond
	defer func() { thinkingCheckInterval = origInterval }()

	cfg := &config.Config{Model: "thinker", ContextWindow: 4096}
	stubJev := &stubJevDecider{
		enabled:            true,
		model:              "jev-v1",
		thinkingLoopResult: true,
	}

	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(&config.Config{YesMode: true}), session.New(cfg), &fakeUI{})
	ag.SetJevstyle(stubJev)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := make(chan ollama.Chunk)
	go func() {
		// Emit thinking chunks slowly so interval fires
		for i := 0; i < 5; i++ {
			stream <- ollama.Chunk{Thinking: "Thinking paragraph repeated\n\n"}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_, _, err := ag.streamAssistantResponse(ctx, cancel, stream)
	if err == nil || !strings.Contains(err.Error(), "thinking loop detected by JEV") {
		t.Fatalf("expected ErrThinkingLoopDetected, got: %v", err)
	}
}

func TestThinkingNoJevTimeout(t *testing.T) {
	origTimeout := thinkingNoJevTimeout
	thinkingNoJevTimeout = 30 * time.Millisecond
	defer func() { thinkingNoJevTimeout = origTimeout }()

	cfg := &config.Config{Model: "thinker", ContextWindow: 4096}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(&config.Config{YesMode: true}), session.New(cfg), &fakeUI{})
	// No JEV configured

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := make(chan ollama.Chunk)
	go func() {
		for i := 0; i < 10; i++ {
			stream <- ollama.Chunk{Thinking: "Still thinking without JEV...\n"}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_, _, err := ag.streamAssistantResponse(ctx, cancel, stream)
	if err == nil || !IsThinkingTimeoutNoJevErr(err) {
		t.Fatalf("expected ErrThinkingTimeoutNoJev, got: %v", err)
	}
}

type loopTestClient struct {
	calls            int32
	loopAttemptsTill int32 // will loop if calls <= loopAttemptsTill
}

func (c *loopTestClient) Chat(_ context.Context, _ ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	num := atomic.AddInt32(&c.calls, 1)
	ch := make(chan ollama.Chunk, 10)
	go func() {
		if num <= atomic.LoadInt32(&c.loopAttemptsTill) {
			// Emit thinking chunks then delay so interval triggers loop
			for i := 0; i < 5; i++ {
				ch <- ollama.Chunk{Thinking: "Looping thought\n\n"}
				time.Sleep(10 * time.Millisecond)
			}
		} else {
			// Normal answer
			ch <- ollama.Chunk{Thinking: "Valid thought\n\n"}
			time.Sleep(5 * time.Millisecond)
			ch <- ollama.Chunk{Delta: "Hello world!", Done: true}
			close(ch)
		}
	}()
	return ch, nil
}
func (c *loopTestClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{}, nil
}
func (c *loopTestClient) Tags(context.Context) ([]ollama.Model, error) {
	return nil, nil
}
func (c *loopTestClient) Version(context.Context) (string, error) {
	return "", nil
}
func (c *loopTestClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return nil
}

func TestChatOnceThinkingLoopRetriesSucceeds(t *testing.T) {
	origInterval := thinkingCheckInterval
	thinkingCheckInterval = 25 * time.Millisecond
	defer func() { thinkingCheckInterval = origInterval }()

	cfg := &config.Config{Model: "thinker", ContextWindow: 4096}
	client := &loopTestClient{loopAttemptsTill: 1} // loops on attempt 1, succeeds on attempt 2
	stubJev := &stubJevDecider{
		enabled:            true,
		model:              "jev-v1",
		thinkingLoopResult: true,
	}

	ag := New(cfg, client, tools.NewRegistry(), permissions.NewManager(&config.Config{YesMode: true}), session.New(cfg), &fakeUI{})
	ag.SetJevstyle(stubJev)

	reply, _, err := ag.chatOnce(context.Background())
	if err != nil {
		t.Fatalf("expected successful reply on retry 2, got err: %v", err)
	}
	if !strings.Contains(reply, "Hello world!") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if calls := atomic.LoadInt32(&client.calls); calls != 2 {
		t.Fatalf("expected exactly 2 chat calls, got %d", calls)
	}
}

func TestChatOnceThinkingLoopExceedsLimit(t *testing.T) {
	origInterval := thinkingCheckInterval
	thinkingCheckInterval = 25 * time.Millisecond
	defer func() { thinkingCheckInterval = origInterval }()

	cfg := &config.Config{Model: "thinker", ContextWindow: 4096}
	client := &loopTestClient{loopAttemptsTill: 10} // always loops
	stubJev := &stubJevDecider{
		enabled:            true,
		model:              "jev-v1",
		thinkingLoopResult: true,
	}

	ag := New(cfg, client, tools.NewRegistry(), permissions.NewManager(&config.Config{YesMode: true}), session.New(cfg), &fakeUI{})
	ag.SetJevstyle(stubJev)

	_, _, err := ag.chatOnce(context.Background())
	if err == nil {
		t.Fatal("expected ThinkingLoopLimitError, got nil")
	}
	if !IsThinkingLoopLimitErr(err) {
		t.Fatalf("expected IsThinkingLoopLimitErr=true, got err: %v", err)
	}
	if calls := atomic.LoadInt32(&client.calls); calls != 3 {
		t.Fatalf("expected exactly 3 chat calls before returning limit error, got %d", calls)
	}
}

func TestRunStepNewStepOnThinkingLoop(t *testing.T) {
	origInterval := thinkingCheckInterval
	thinkingCheckInterval = 25 * time.Millisecond
	defer func() { thinkingCheckInterval = origInterval }()

	cfg := &config.Config{Model: "thinker", ContextWindow: 4096}
	// Attempt 1, 2, 3 in step 1 loop.
	// Step 2 starts (call 4), and succeeds!
	client := &loopTestClient{loopAttemptsTill: 3}
	stubJev := &stubJevDecider{
		enabled:            true,
		model:              "jev-v1",
		thinkingLoopResult: true,
	}

	sess := session.New(cfg)
	ag := New(cfg, client, tools.NewRegistry(), permissions.NewManager(&config.Config{YesMode: true}), sess, &fakeUI{})
	ag.SetJevstyle(stubJev)

	err := ag.Run(context.Background(), "solve task")
	if err != nil {
		t.Fatalf("expected Run to succeed on step 2, got err: %v", err)
	}

	// Verify that a runtime reminder was added to the session after the loop limit was hit in step 1
	var foundReminder bool
	for _, m := range sess.MessagesReadOnly() {
		if strings.Contains(m.Content, "Thinking loop error") {
			foundReminder = true
			break
		}
	}
	if !foundReminder {
		t.Fatal("expected runtime reminder about thinking loop to be injected into session")
	}

	// Total calls: 3 in step 1 (which failed and triggered new step) + 1 in step 2 = 4
	if calls := atomic.LoadInt32(&client.calls); calls != 4 {
		t.Fatalf("expected 4 calls total across the two steps, got %d", calls)
	}
}
