package ollama

import "testing"

// Ollama prefixes the GGUF context length with the architecture name, and the
// prefix changes per model family.
func TestContextLengthFromModelInfo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		info map[string]any
		want int
	}{
		{"prefixed", map[string]any{"qwen35.context_length": float64(262144)}, 262144},
		{"llama", map[string]any{"llama.context_length": float64(8192)}, 8192},
		{"plain", map[string]any{"context_length": 32768}, 32768},
		{"missing", map[string]any{"general.architecture": "qwen35"}, 0},
		{"nil", nil, 0},
	}
	for _, tc := range cases {
		if got := contextLengthFromModelInfo(tc.info); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
