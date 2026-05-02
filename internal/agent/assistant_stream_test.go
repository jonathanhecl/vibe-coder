package agent

import (
	"strings"
	"testing"
)

func TestToolEnvelopeByteIndex(t *testing.T) {
	t.Parallel()
	if toolEnvelopeByteIndex("Hello world") >= 0 {
		t.Fatal("expected no tool")
	}
	if i := toolEnvelopeByteIndex("Ok.\n<invoke name=\"Glob\">"); i < 0 || i > 4 {
		t.Fatalf("expected <invoke index, got %d", i)
	}
	if i := toolEnvelopeByteIndex("<Glob>"); i != 0 {
		t.Fatalf("got %d", i)
	}
	if i := toolEnvelopeByteIndex("<INVOKE name=\"Glob\">"); i != 0 {
		t.Fatalf("case-fold invoke: got %d", i)
	}
}

// TestToolEnvelopeIncrementalScan simulates streaming chunks: only the suffix
// starting at (prevLen - toolEnvelopeScanOverlap) must be scanned to find a
// tool envelope that straddles chunk boundaries.
func TestToolEnvelopeIncrementalScan(t *testing.T) {
	t.Parallel()
	prefix := strings.Repeat("a", toolEnvelopeScanOverlap+3)
	chunk1 := prefix + "<inv"
	chunk2 := "oke name=\"Glob\">{}</invoke>"
	var buf []byte
	prevLen := len(buf)
	buf = append(buf, chunk1...)
	from := 0
	if prevLen > 0 {
		from = prevLen - toolEnvelopeScanOverlap
		if from < 0 {
			from = 0
		}
	}
	if toolEnvelopeByteIndex(string(buf[from:])) >= 0 {
		t.Fatal("expected no complete envelope yet")
	}
	prevLen = len(buf)
	buf = append(buf, chunk2...)
	from = 0
	if prevLen > 0 {
		from = prevLen - toolEnvelopeScanOverlap
		if from < 0 {
			from = 0
		}
	}
	rel := toolEnvelopeByteIndex(string(buf[from:]))
	if rel < 0 {
		t.Fatal("expected envelope in suffix scan")
	}
	cut := from + rel
	want := len(prefix)
	if cut != want {
		t.Fatalf("cut index: got %d want %d", cut, want)
	}
}

func TestAssistantTextAfterFirstClosedTool(t *testing.T) {
	t.Parallel()
	s := `Here goes.

<invoke name="Read">{"file_path":"a.go"}</invoke>

Thanks for reading.`
	if got := assistantTextAfterFirstClosedTool(s); got != "Thanks for reading." {
		t.Fatalf("got %q", got)
	}
	if assistantTextAfterFirstClosedTool(`<invoke name="X">{}</invoke>`) != "" {
		t.Fatal("expected empty tail")
	}
}

func TestAssistantVisibleTextStripsThinking(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{in: "<think>internal</think>", want: ""},
		{in: "Hola\n<thinking>internal</thinking>", want: "Hola"},
		{in: "A <think>x</think> B", want: "A  B"},
		{in: "<invoke name=\"Read\">{\"file_path\":\"a.go\"}</invoke>", want: ""},
		{in: "Texto <invoke name=\"Read\">{\"file_path\":\"a.go\"}</invoke>", want: "Texto"},
	}
	for _, tc := range cases {
		if got := assistantVisibleText(tc.in); got != tc.want {
			t.Fatalf("assistantVisibleText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
