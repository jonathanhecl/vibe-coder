package tools

import (
	"strings"
	"testing"
)

func TestHTMLToTextStripsScriptsAndEntities(t *testing.T) {
	doc := "<html><head><script >var x=1;</script><style>a{}</style>" +
		"<noscript>n</noscript></head><body>Hello &amp; World<!-- c --></body></html>"
	got := htmlToText(doc)
	if strings.Contains(got, "var x") || strings.Contains(got, "a{}") {
		t.Fatalf("script/style content leaked into text: %q", got)
	}
	if !strings.Contains(got, "Hello & World") {
		t.Fatalf("HTML entities not decoded: %q", got)
	}
}
