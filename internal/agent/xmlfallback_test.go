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
