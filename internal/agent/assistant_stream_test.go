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
