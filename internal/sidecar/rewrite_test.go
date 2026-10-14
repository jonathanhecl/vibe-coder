package sidecar

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestSanitizeQuery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "auth token refresh", "auth token refresh"},
		{"surrounding spaces", "   auth token   ", "auth token"},
		{"quoted first line", "\"auth token\"\nsecond line", "auth token"},
		{"bullet", "- auth token", "auth token"},
		{"heading", "## auth token", "auth token"},
		{"leading blank lines", "\n\n  auth token", "auth token"},
		{"empty", "", ""},
		{"only whitespace", "\n   \n", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeQuery(tc.raw); got != tc.want {
				t.Fatalf("sanitizeQuery(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSanitizeQueryCapsLength(t *testing.T) {
	t.Parallel()

	got := sanitizeQuery(strings.Repeat("x", maxRewriteRunes*2))
	if runes := []rune(got); len(runes) != maxRewriteRunes {
		t.Fatalf("expected %d runes, got %d", maxRewriteRunes, len(runes))
	}
}

func TestRewriteQueryWithoutSidecar(t *testing.T) {
	t.Parallel()

	var nilPool *Pool
	if got := nilPool.RewriteQuery(context.Background(), "auth"); got != "" {
		t.Fatalf("a nil pool should rewrite nothing, got %q", got)
	}
	disabled := New(&config.Config{}, &fakeClient{reply: "auth"})
	if got := disabled.RewriteQuery(context.Background(), "auth"); got != "" {
		t.Fatalf("a disabled sidecar should rewrite nothing, got %q", got)
	}
	if got := disabled.RewriteQuery(context.Background(), "   "); got != "" {
		t.Fatalf("a blank query should rewrite nothing, got %q", got)
	}
}

func TestRewriteQueryUsesSidecar(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{SidecarModel: "sidecar"}
	pool := New(cfg, &fakeClient{reply: "\"auth token refresh\"\n"})
	if got := pool.RewriteQuery(context.Background(), "please fix the auth token refresh bug"); got != "auth token refresh" {
		t.Fatalf("unexpected rewrite: %q", got)
	}
}
