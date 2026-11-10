package ollama

import "testing"

func TestNumCtxFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		promptChars int
		numPredict  int
		capTokens   int
		want        int
	}{
		{"empty prompt floors at 2048", 0, 0, 0, 2048},
		{"small prompt floors at 2048", 100, 50, 0, 2048},
		{"rounds up to 1024 boundary", 3000, 1000, 0, 3072},
		{"larger prompt rounds up", 9000, 512, 0, 4096},
		{"caps at capTokens", 100000, 1000, 8192, 8192},
		{"cap below floor wins", 0, 0, 1024, 1024},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NumCtxFor(tc.promptChars, tc.numPredict, tc.capTokens); got != tc.want {
				t.Fatalf("NumCtxFor(%d, %d, %d) = %d, want %d", tc.promptChars, tc.numPredict, tc.capTokens, got, tc.want)
			}
		})
	}
}

func TestNumCtxCap(t *testing.T) {
	t.Parallel()
	if got := NumCtxCap(4096); got != 4096 {
		t.Fatalf("NumCtxCap(4096) = %d", got)
	}
	if got := NumCtxCap(0); got != defaultNumCtxCap {
		t.Fatalf("NumCtxCap(0) = %d, want %d", got, defaultNumCtxCap)
	}
	if got := NumCtxCap(-5); got != defaultNumCtxCap {
		t.Fatalf("NumCtxCap(-5) = %d, want %d", got, defaultNumCtxCap)
	}
}
