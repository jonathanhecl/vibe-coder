package agent

import "testing"

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
