package text

import "testing"

func TestNormalize(t *testing.T) {
	if got := Normalize("  go  "); got != "GO" {
		t.Fatalf("Normalize(%q) = %q, want %q", "  go  ", got, "GO")
	}
	if got := Normalize("Mixed Case"); got != "MIXED CASE" {
		t.Fatalf("Normalize(%q) = %q, want %q", "Mixed Case", got, "MIXED CASE")
	}
}
