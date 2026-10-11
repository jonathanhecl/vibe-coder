package ollama

import (
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"mac-mini.local", "http://mac-mini.local:11434"},
		{"mac-mini.local:11434", "http://mac-mini.local:11434"},
		{"http://mac-mini.local", "http://mac-mini.local:11434"},
		{"http://mac-mini.local/", "http://mac-mini.local:11434"},
		{"http://mac-mini.local:11434", "http://mac-mini.local:11434"},
		{"http://mac-mini.local:80", "http://mac-mini.local:80"},
		{"http://mac-mini.local:8080", "http://mac-mini.local:8080"},
		{"http://192.168.0.33", "http://192.168.0.33:11434"},
		{"192.168.0.33", "http://192.168.0.33:11434"},
		{"192.168.0.33:11434", "http://192.168.0.33:11434"},
		{"http://localhost", "http://localhost:11434"},
		{"http://localhost:11434", "http://localhost:11434"},
		{"localhost", "http://localhost:11434"},
		{"https://ollama.example.com", "https://ollama.example.com"},
		{"https://ollama.example.com:11434", "https://ollama.example.com:11434"},
		{"http://[::1]", "http://[::1]:11434"},
	}

	for _, tc := range cases {
		got := NormalizeURL(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
