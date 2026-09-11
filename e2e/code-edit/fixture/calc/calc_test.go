package calc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
	if got := Add(-1, 1); got != 0 {
		t.Fatalf("Add(-1, 1) = %d, want 0", got)
	}
}

func TestMax(t *testing.T) {
	if got := Max(1, 5); got != 5 {
		t.Fatalf("Max(1, 5) = %d, want 5", got)
	}
	if got := Max(9, 2); got != 9 {
		t.Fatalf("Max(9, 2) = %d, want 9", got)
	}
}
