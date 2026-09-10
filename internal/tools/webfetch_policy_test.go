package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestValidateFetchURLBlocksPrivateAndBadSchemes(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"ftp://example.com",
		"http://localhost:8080/",
		"http://127.0.0.1/",
		"http://169.254.169.254/latest/meta-data",
		"http://user@127.0.0.1/",
		"",
		"http://",
	} {
		if err := validateFetchURL(raw); err == nil {
			t.Fatalf("expected rejection for %q", raw)
		}
	}
	if err := validateFetchURL("https://example.com/page"); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func TestFetchRedirectPolicyBlocksPrivateTargets(t *testing.T) {
	t.Parallel()
	client := newWebFetchClient()
	must := func(raw string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	if err := client.CheckRedirect(must("http://10.0.0.1/internal"), []*http.Request{must("https://example.com/")}); err == nil {
		t.Fatal("expected private redirect target to be blocked")
	}
	via := make([]*http.Request, 0, maxFetchRedirects)
	for i := 0; i < maxFetchRedirects; i++ {
		via = append(via, must("https://example.com/"+strings.Repeat("x", i)))
	}
	if err := client.CheckRedirect(must("https://example.com/next"), via); err == nil {
		t.Fatal("expected redirect limit error")
	}
}

func TestWebFetchContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := NewWebFetchTool().Execute(ctx, map[string]any{"url": "https://example.com/"})
	if !out.IsError || !strings.Contains(out.Output, "cancel") {
		t.Fatalf("expected cancellation error, got %+v", out)
	}
}
