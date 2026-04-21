package agent

import "testing"

func TestParseXMLFallbackInvoke(t *testing.T) {
	name, params, ok := parseXMLFallback(`<invoke name="Glob">{"pattern":"*.go","path":"./internal"}</invoke>`)
	if !ok || name != "Glob" {
		t.Fatalf("expected invoke parse, got ok=%t name=%q", ok, name)
	}
	if params["pattern"] != "*.go" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestParseXMLFallbackToolTag(t *testing.T) {
	name, params, ok := parseXMLFallback(`<READ>{"file_path":"/tmp/a.txt",}</READ>`)
	if !ok || name != "Read" {
		t.Fatalf("expected tool tag parse, got ok=%t name=%q", ok, name)
	}
	if params["file_path"] != "/tmp/a.txt" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

// Regression: the model emitted a glob pattern containing braces inside the
// JSON string. The previous non-greedy regex stopped at the first '}' of
// "{gd,cs,...}" and produced invalid JSON, so the agent silently dropped the
// tool call and ended its turn.
func TestParseXMLFallbackInvokeBracesInsideStringValue(t *testing.T) {
	in := `<invoke name="Glob">{"pattern":"**/*.{gd,cs,gdshader,gdext,gdnlib}"}</invoke>`
	name, params, ok := parseXMLFallback(in)
	if !ok || name != "Glob" {
		t.Fatalf("expected invoke parse with nested braces, got ok=%t name=%q", ok, name)
	}
	if params["pattern"] != "**/*.{gd,cs,gdshader,gdext,gdnlib}" {
		t.Fatalf("unexpected pattern: %#v", params["pattern"])
	}
}

// Regression: the model appended an extra '"' between the JSON object and the
// closing tag (e.g. `..."}"</invoke>`). The parser must tolerate trailing
// junk after the balanced JSON body.
func TestParseXMLFallbackInvokeTrailingJunkBeforeClose(t *testing.T) {
	in := `<invoke name="Glob">{"pattern":"**/*.{gd,cs,gdshader,gdext,gdnlib}"}"</invoke>`
	name, params, ok := parseXMLFallback(in)
	if !ok || name != "Glob" {
		t.Fatalf("expected invoke parse with trailing junk, got ok=%t name=%q", ok, name)
	}
	if params["pattern"] != "**/*.{gd,cs,gdshader,gdext,gdnlib}" {
		t.Fatalf("unexpected pattern: %#v", params["pattern"])
	}
}

// Strings inside the JSON body may contain escaped quotes; the brace scanner
// must respect string boundaries.
func TestParseXMLFallbackEscapedQuotesInString(t *testing.T) {
	in := `<invoke name="Bash">{"command":"echo \"hi}\""}</invoke>`
	name, params, ok := parseXMLFallback(in)
	if !ok || name != "Bash" {
		t.Fatalf("expected invoke parse with escaped quotes, got ok=%t name=%q", ok, name)
	}
	if params["command"] != `echo "hi}"` {
		t.Fatalf("unexpected command: %#v", params["command"])
	}
}

func TestParseXMLFallbackToolCallEnvelope(t *testing.T) {
	in := `<tool_call name="Glob">{"pattern":"*.md"}</tool_call>`
	name, params, ok := parseXMLFallback(in)
	if !ok || name != "Glob" {
		t.Fatalf("expected tool_call parse, got ok=%t name=%q", ok, name)
	}
	if params["pattern"] != "*.md" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

// Surrounding prose around the envelope must not prevent recognition; the
// loop already streams the reply, but recovery should still succeed.
func TestParseXMLFallbackInvokeWithSurroundingText(t *testing.T) {
	in := "Sure, let me look:\n<invoke name=\"Glob\">{\"pattern\":\"*.go\"}</invoke>\nthanks"
	name, params, ok := parseXMLFallback(in)
	if !ok || name != "Glob" {
		t.Fatalf("expected invoke parse with surrounding text, got ok=%t name=%q", ok, name)
	}
	if params["pattern"] != "*.go" {
		t.Fatalf("unexpected params: %#v", params)
	}
}
