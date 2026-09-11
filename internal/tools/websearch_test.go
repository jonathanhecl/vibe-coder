package tools

import (
	"encoding/base64"
	"net/url"
	"testing"
)

func bingRedirect(target string) string {
	enc := "a1" + base64.RawURLEncoding.EncodeToString([]byte(target))
	return "https://www.bing.com/ck/a?!&&p=abc&u=" + url.QueryEscape(enc) + "&ntb=1"
}

func TestDecodeBingLink(t *testing.T) {
	target := "https://go.dev/doc/"
	if got := decodeBingLink(bingRedirect(target)); got != target {
		t.Fatalf("decodeBingLink redirect = %q, want %q", got, target)
	}
	plain := "https://example.com/page"
	if got := decodeBingLink(plain); got != plain {
		t.Fatalf("decodeBingLink plain = %q, want %q", got, plain)
	}
	if got := decodeBingLink("https://www.bing.com/search?q=x"); got != "https://www.bing.com/search?q=x" {
		t.Fatalf("decodeBingLink bing-without-u = %q", got)
	}
}

func TestNormalizeDDGLink(t *testing.T) {
	raw := "//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&rut=abc"
	if got := normalizeDDGLink(raw); got != "https://go.dev/doc/" {
		t.Fatalf("normalizeDDGLink redirect = %q", got)
	}
	direct := "https://example.com/page?a=1"
	if got := normalizeDDGLink(direct); got != direct {
		t.Fatalf("normalizeDDGLink direct = %q", got)
	}
}

func TestParseBingResults(t *testing.T) {
	doc := `<html><body>` +
		`<li class="b_algo"><link rel="stylesheet" href="/x.css"/><h2><a href="` + bingRedirect("https://go.dev/doc/") + `">The Go Programming Language</a></h2></li>` +
		`<li class="b_algo"><h2><a href="https://en.wikipedia.org/wiki/Go_(programming_language)">Go (programming language) - Wikipedia</a></h2></li>` +
		`</body></html>`
	got := parseBingResults(doc)
	want := []string{
		"- The Go Programming Language | https://go.dev/doc/",
		"- Go (programming language) - Wikipedia | https://en.wikipedia.org/wiki/Go_(programming_language)",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result %d = %q, want %q", i, got[i], want[i])
		}
	}
}
