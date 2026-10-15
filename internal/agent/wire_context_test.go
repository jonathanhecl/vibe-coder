package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newWireTestAgent(t *testing.T, ctxWindow, keepResults int) (*Agent, *session.Session) {
	t.Helper()
	cfg := &config.Config{
		Model:         "m",
		ContextWindow: ctxWindow,
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
	}
	cfg.Harness.KeepToolResults = keepResults
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	return New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{}), sess
}

// addNativeToolTurn appends one assistant tool-call turn plus a tool result.
func addNativeToolTurn(sess *session.Session, output string) {
	sess.AddAssistantToolCalls("", []ollama.MessageToolCall{{
		Function: ollama.MessageToolCallFunction{Name: "Read", Arguments: map[string]any{}},
	}})
	sess.AddToolResult("Read", output)
}

func TestComputeBudgetMath(t *testing.T) {
	t.Parallel()
	ag, _ := newWireTestAgent(t, 32768, 6)
	b := ag.computeBudget("SYS")
	if b.Window != 32768 {
		t.Fatalf("Window=%d", b.Window)
	}
	if b.OutputReserve != 4096 {
		t.Fatalf("OutputReserve=%d, want default 4096", b.OutputReserve)
	}
	if b.SystemTokens != session.EstimateTokens("SYS") {
		t.Fatalf("SystemTokens=%d", b.SystemTokens)
	}
	if b.SchemaTokens <= 0 {
		t.Fatal("SchemaTokens should price the native tool schema")
	}
	want := 32768 - 4096 - b.SystemTokens - b.SchemaTokens - 32768*5/100
	if b.Transcript != want {
		t.Fatalf("Transcript=%d, want %d", b.Transcript, want)
	}
}

func TestComputeBudgetFloorsTranscript(t *testing.T) {
	t.Parallel()
	ag, _ := newWireTestAgent(t, 32768, 6)
	b := ag.computeBudget(strings.Repeat("s", 32768*4))
	floor := 32768 / 8
	if b.Transcript != floor {
		t.Fatalf("Transcript=%d, want floor %d", b.Transcript, floor)
	}
}

func TestComputeBudgetCapsOutputReserve(t *testing.T) {
	t.Parallel()
	ag, sess := newWireTestAgent(t, 4096, 6)
	_ = sess
	ag.cfg.MaxTokens = 99999
	b := ag.computeBudget("SYS")
	if b.OutputReserve != 4096/4 {
		t.Fatalf("OutputReserve=%d, want cap %d", b.OutputReserve, 4096/4)
	}
}

func TestMaskKeepsKMostRecentToolTurns(t *testing.T) {
	t.Parallel()
	ag, _ := newWireTestAgent(t, 32768, 3)
	var hist []session.Message
	for i := 0; i < 5; i++ {
		hist = append(hist,
			session.Message{Role: "assistant", Content: fmt.Sprintf("turn %d", i)},
			session.Message{Role: "tool", ToolName: "Read", Content: strings.Repeat("x", 1000)})
	}
	mb := maskBeforeForTurns(toolTurnStarts(hist), 3)
	masked, n := maskToolResults(hist, mb)
	if n != 2 {
		t.Fatalf("masked count=%d, want 2", n)
	}
	for i, m := range masked {
		if m.Role != "tool" {
			continue
		}
		if i < mb && !strings.HasPrefix(m.Content, "[elided]") {
			t.Fatalf("index %d should be masked: %q", i, m.Content[:40])
		}
		if i >= mb && strings.HasPrefix(m.Content, "[elided]") {
			t.Fatalf("index %d must stay unmasked", i)
		}
	}
	_ = ag
}

func TestMaskDoesNotMutateTranscript(t *testing.T) {
	t.Parallel()
	hist := []session.Message{
		{Role: "assistant", Content: "turn"},
		{Role: "tool", ToolName: "Read", Content: strings.Repeat("x", 1000)},
	}
	_, n := maskToolResults(hist, 2)
	if n != 1 {
		t.Fatalf("masked=%d", n)
	}
	if strings.HasPrefix(hist[1].Content, "[elided]") {
		t.Fatal("maskToolResults must not mutate the stored transcript")
	}
}

func TestMaskSkipsSmallOutputs(t *testing.T) {
	t.Parallel()
	hist := []session.Message{
		{Role: "assistant", Content: "turn"},
		{Role: "tool", ToolName: "Read", Content: strings.Repeat("x", 100)},
	}
	masked, n := maskToolResults(hist, 2)
	if n != 0 || masked[1].Content != hist[1].Content {
		t.Fatalf("small output must not be masked: n=%d content=%q", n, masked[1].Content)
	}
}

func TestMaskNativeStubShape(t *testing.T) {
	t.Parallel()
	out := "first result line\n" + strings.Repeat("x", 900)
	hist := []session.Message{
		{Role: "assistant", Content: "turn"},
		{Role: "tool", ToolName: "Read", Content: out},
	}
	masked, n := maskToolResults(hist, 2)
	if n != 1 {
		t.Fatalf("masked=%d", n)
	}
	want := fmt.Sprintf("[elided] Read output (%d chars) from an earlier step. First line: first result line", len(out))
	if masked[1].Content != want {
		t.Fatalf("stub = %q, want %q", masked[1].Content, want)
	}
	if masked[1].ToolName != "Read" {
		t.Fatalf("ToolName lost: %q", masked[1].ToolName)
	}
}

func TestMaskXMLStubShape(t *testing.T) {
	t.Parallel()
	out := "first line\n" + strings.Repeat("y", 900)
	hist := []session.Message{
		{Role: "assistant", Content: "turn"},
		{Role: "user", Content: session.ToolObservationUserContent("Read", out)},
	}
	masked, n := maskToolResults(hist, 2)
	if n != 1 {
		t.Fatalf("masked=%d", n)
	}
	if !strings.Contains(masked[1].Content, "[tool_result name=Read]") ||
		!strings.Contains(masked[1].Content, "[elided] Read output") ||
		!strings.Contains(masked[1].Content, "First line: first line") {
		t.Fatalf("XML stub wrong: %q", masked[1].Content)
	}
}

func TestMaskHysteresisBoundaries(t *testing.T) {
	t.Parallel()
	ag, sess := newWireTestAgent(t, 32768, 3)
	var build func() (int, int)
	build = func() (int, int) {
		hist := sess.MessagesReadOnly()
		gen := sess.Generation()
		ag.wireMu.Lock()
		mb := ag.maskBeforeLocked(hist, gen)
		ag.wireMu.Unlock()
		return mb, len(toolTurnStarts(hist))
	}
	// Prime state with K turns.
	for i := 0; i < 3; i++ {
		addNativeToolTurn(sess, strings.Repeat("x", 600))
	}
	mb, turns := build()
	if mb != -1 || turns != 3 {
		t.Fatalf("initial maskBefore=%d turns=%d", mb, turns)
	}
	// K+1 .. K+4 unmasked turns: maskBefore stays fixed.
	for extra := 1; extra <= 4; extra++ {
		addNativeToolTurn(sess, strings.Repeat("x", 600))
		mb, _ = build()
		if mb != -1 {
			t.Fatalf("maskBefore moved at K+%d: %d", extra, mb)
		}
	}
	// K+5: recompute to exactly K unmasked turns.
	addNativeToolTurn(sess, strings.Repeat("x", 600))
	mb, turns = build()
	if mb <= 0 {
		t.Fatalf("maskBefore should advance at K+5, got %d", mb)
	}
	unmasked := 0
	for _, s := range toolTurnStarts(sess.MessagesReadOnly()) {
		if s >= mb {
			unmasked++
		}
	}
	if unmasked != 3 {
		t.Fatalf("unmasked turns=%d, want K=3", unmasked)
	}
}

func TestMaskResetOnGenerationChange(t *testing.T) {
	t.Parallel()
	ag, sess := newWireTestAgent(t, 32768, 2)
	for i := 0; i < 4; i++ {
		addNativeToolTurn(sess, strings.Repeat("x", 600))
	}
	hist := sess.MessagesReadOnly()
	ag.wireMu.Lock()
	_ = ag.maskBeforeLocked(hist, sess.Generation())
	// Fake drift: pretend an append built up many unmasked turns, then a
	// compaction rewrote the transcript (generation bump resets state).
	ag.wire.maskBefore = 999
	ag.wireMu.Unlock()
	sess.Clear()
	for i := 0; i < 2; i++ {
		addNativeToolTurn(sess, "short")
	}
	hist = sess.MessagesReadOnly()
	ag.wireMu.Lock()
	mb := ag.maskBeforeLocked(hist, sess.Generation())
	ag.wireMu.Unlock()
	if mb != -1 {
		t.Fatalf("maskBefore=%d after generation change, want -1 (nothing masked)", mb)
	}
}

func TestStickyWindowStableAcrossAppends(t *testing.T) {
	t.Parallel()
	ag, sess := newWireTestAgent(t, 32768, 6)
	sess.AddUser("do the thing")
	_, s1 := ag.buildOllamaMessages(context.Background(), "SYS")
	sess.AddAssistant("step one")
	_, s2 := ag.buildOllamaMessages(context.Background(), "SYS")
	sess.AddAssistant("step two")
	_, s3 := ag.buildOllamaMessages(context.Background(), "SYS")
	if s1.WindowStart != s2.WindowStart || s2.WindowStart != s3.WindowStart {
		t.Fatalf("sticky start moved: %d -> %d -> %d", s1.WindowStart, s2.WindowStart, s3.WindowStart)
	}
	if s1.WindowStart != 0 {
		t.Fatalf("WindowStart=%d, want 0 (history fits)", s1.WindowStart)
	}
}

func TestWindowRecomputeStaysUnderBudget(t *testing.T) {
	t.Parallel()
	ag, sess := newWireTestAgent(t, 16384, 6)
	sess.AddUser("fill")
	for i := 0; i < 60; i++ {
		addNativeToolTurn(sess, strings.Repeat("z", 4000))
	}
	msgs, stats := ag.buildOllamaMessages(context.Background(), "SYS")
	budget := ag.computeBudget("SYS")
	wireTokens := stats.TranscriptTokensEst
	limit := budget.Transcript * 60 / 100
	if wireTokens > limit {
		t.Fatalf("wire transcript %d tokens exceeds 60%% of budget %d (limit %d)", wireTokens, budget.Transcript, limit)
	}
	if stats.WindowStart <= 0 {
		t.Fatal("window should have been trimmed")
	}
	if stats.MessagesDropped != stats.WindowStart {
		t.Fatalf("MessagesDropped=%d want %d", stats.MessagesDropped, stats.WindowStart)
	}
	if stats.MessagesSent != len(msgs)-1 {
		t.Fatalf("MessagesSent=%d", stats.MessagesSent)
	}
}

// TestMaskingSimulation is the deterministic 20-turn scenario the lead asked
// for: ContextWindow 32768, twenty Read turns of 8KB output each. Logs the
// wire transcript estimate, masked count, and window start.
func TestMaskingSimulation(t *testing.T) {
	ag, sess := newWireTestAgent(t, 32768, 6)
	sess.AddUser("simulate twenty reads")
	for i := 0; i < 20; i++ {
		addNativeToolTurn(sess, strings.Repeat("r", 8*1024))
	}
	msgs, stats := ag.buildOllamaMessages(context.Background(), ag.buildSystemPrompt())
	t.Logf("wire transcript tokens=%d masked=%d window_start=%d messages_sent=%d",
		stats.TranscriptTokensEst, stats.ToolResultsMasked, stats.WindowStart, stats.MessagesSent)
	if len(msgs) < 2 {
		t.Fatal("no transcript messages sent")
	}
}

// TestCompactBestEffortTriggersOnMaskedTokens verifies the compaction trigger
// prices the masked transcript: history whose raw size exceeds the threshold
// but whose masked form fits must not compact.
func TestCompactBestEffortUsesMaskedTokens(t *testing.T) {
	ag, sess := newWireTestAgent(t, 16384, 2)
	ag.cfg.Harness.CompactAtPercent = 50
	sess.SetClient(fakeClient{})
	sess.AddUser("task")
	// Many old turns of large output: raw transcript >> threshold, but
	// masking collapses the old outputs to stubs.
	for i := 0; i < 30; i++ {
		addNativeToolTurn(sess, strings.Repeat("z", 4000))
	}
	sess.AddAssistant("done")
	genBefore := sess.Generation()
	// Prime the wire state so compaction sees the same masking as the wire.
	_, _ = ag.buildOllamaMessages(context.Background(), ag.buildSystemPrompt())
	ag.compactBestEffort(context.Background())
	if sess.Generation() != genBefore {
		t.Fatal("compaction fired even though masked transcript fits the budget")
	}
}

func TestCompactBestEffortFiresWhenMaskedTooBig(t *testing.T) {
	ag, sess := newWireTestAgent(t, 16384, 30)
	ag.cfg.Harness.CompactAtPercent = 50
	sess.SetClient(fakeClient{})
	sess.AddUser("task")
	// KeepToolResults=30 keeps every output verbatim; masked cost == raw cost
	// and exceeds the budget percentage, so compaction fires.
	for i := 0; i < 30; i++ {
		addNativeToolTurn(sess, strings.Repeat("z", 4000))
	}
	genBefore := sess.Generation()
	_, _ = ag.buildOllamaMessages(context.Background(), ag.buildSystemPrompt())
	ag.compactBestEffort(context.Background())
	if sess.Generation() == genBefore {
		t.Fatal("compaction did not fire despite oversized masked transcript")
	}
	if !ag.compactedPending.Load() {
		t.Fatal("compactedPending not set for next metrics entry")
	}
}

func TestRecordChatAttemptIncludesToolSchemaInCalibration(t *testing.T) {
	t.Parallel()
	ag, sess := newWireTestAgent(t, 32768, 6)
	ag.cfg.ToolsKnown = true
	ag.cfg.ToolsSupported = true
	ag.reg.RegisterDefaults()
	sess.AddUser("short prompt")

	sys := ag.buildSystemPrompt()
	_, wstats := ag.buildOllamaMessages(context.Background(), sys)
	sysEst := session.EstimateTokens(sys)
	schemaEst := ag.toolSchemaTokens()
	if schemaEst <= 0 {
		t.Fatal("expected positive toolSchemaTokens with default tools registered")
	}
	// Provider reports exact 1:1 token usage including system + tool schemas + transcript.
	ag.lastUsage = &ollama.Usage{
		PromptTokens:     sysEst + schemaEst + wstats.TranscriptTokensEst,
		CompletionTokens: 16,
	}
	ag.recordChatAttempt(sys, wstats, time.Now(), 0, nil)

	if got := sess.CalibrateTokens(1000); got != 1000 {
		t.Fatalf("CalibrateTokens(1000)=%d, want 1000 when provider PromptTokens matches system+schema+transcript", got)
	}
}

func TestCalibratedTokensAffectComputeBudgetAndWindow(t *testing.T) {
	t.Parallel()
	ag, sess := newWireTestAgent(t, 16384, 6)
	ag.reg.RegisterDefaults()

	uncalibrated := ag.computeBudget("SYS")
	for i := 0; i < 20; i++ {
		addNativeToolTurn(sess, strings.Repeat("a", 1200))
	}
	_, statsBefore := ag.buildOllamaMessages(context.Background(), "SYS")

	// Teach the session that the provider counts 2x as many tokens as the heuristic.
	sess.ObservePromptUsage(100, 200)
	calibrated := ag.computeBudget("SYS")
	if calibrated.SchemaTokens <= uncalibrated.SchemaTokens {
		t.Fatalf("calibrated SchemaTokens=%d should exceed uncalibrated %d", calibrated.SchemaTokens, uncalibrated.SchemaTokens)
	}
	if calibrated.Transcript >= uncalibrated.Transcript {
		t.Fatalf("calibrated Transcript=%d should be smaller than uncalibrated %d", calibrated.Transcript, uncalibrated.Transcript)
	}

	// Force window recompute under the 2x calibration.
	ag.wireMu.Lock()
	ag.wire.set = false
	ag.wireMu.Unlock()
	_, statsAfter := ag.buildOllamaMessages(context.Background(), "SYS")
	if statsAfter.WindowStart <= statsBefore.WindowStart {
		t.Fatalf("expected calibrated window to trim earlier: before=%d after=%d", statsBefore.WindowStart, statsAfter.WindowStart)
	}
}
