package tools

import (
	"strings"
	"testing"
)

func TestAttemptedArgsSummary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		params map[string]any
		want   []string
	}{
		{"nil", nil, []string{"(none)"}},
		{"empty", map[string]any{}, []string{"(none)"}},
		{"small", map[string]any{"file_path": "a.txt"}, []string{"attempted args", "a.txt"}},
		{"parse-error-only", map[string]any{ParseErrorKey: "boom", RawArgsKey: `{"x":`}, []string{"(none)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := AttemptedArgsSummary(tt.params)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("summary %q does not contain %q", got, w)
				}
			}
		})
	}
}

func TestAttemptedArgsSummaryTruncates(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 2000)
	got := AttemptedArgsSummary(map[string]any{"contents": big})
	if !strings.Contains(got, "attempted args") {
		t.Fatalf("missing prefix: %q", got)
	}
	if len([]rune(got)) > AttemptedArgsMaxRunes+100 {
		t.Errorf("summary too long: %d runes", len([]rune(got)))
	}
}

func TestMissingParamErrorIncludesAttempt(t *testing.T) {
	t.Parallel()
	res := MissingParamError("file_path", map[string]any{"contents": "hi"})
	if !res.IsError {
		t.Fatal("want IsError")
	}
	if !strings.Contains(res.Output, "file_path is required") {
		t.Errorf("missing required text: %q", res.Output)
	}
	if !strings.Contains(res.Output, "attempted args") {
		t.Errorf("missing attempted args: %q", res.Output)
	}
}

func TestTruncateRawArgs(t *testing.T) {
	t.Parallel()
	if got := TruncateRawArgs(""); got != "(empty)" {
		t.Errorf("empty = %q", got)
	}
	short := `{"file_path":"a.txt"}`
	if got := TruncateRawArgs(short); got != short {
		t.Errorf("short rewritten: %q", got)
	}
	big := strings.Repeat("y", RawArgsMaxRunes+10)
	got := TruncateRawArgs(big)
	if !strings.Contains(got, "truncated") {
		t.Errorf("big should note truncation: %q", got[:100])
	}
}
