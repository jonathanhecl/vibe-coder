package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditExactMatch(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(p, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "world",
		"new_string": "vibe",
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\nvibe\n" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestEditCRLFWithLFOldString(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "win.txt")
	if err := os.WriteFile(p, []byte("line1\r\nline2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "line2",
		"new_string": "replaced",
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\r\nreplaced\r\n"
	if string(data) != want {
		t.Fatalf("content mismatch:\ngot:  %q\nwant: %q", string(data), want)
	}
}

func TestEditTrimmedWhitespaceFallback(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Model sends old_string with accidental surrounding whitespace.
	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": " beta ",
		"new_string": "delta",
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\ndelta\ngamma\n"
	if string(data) != want {
		t.Fatalf("content mismatch:\ngot:  %q\nwant: %q", string(data), want)
	}
}

func TestEditLineRange(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "c.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "",
		"new_string": "replaced",
		"start_line": 2,
		"end_line":   3,
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "one\nreplaced\nfour\n"
	if string(data) != want {
		t.Fatalf("content mismatch:\ngot:  %q\nwant: %q", string(data), want)
	}
}

func TestEditLineRangeSingleLine(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "d.txt")
	if err := os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "",
		"new_string": "BETA",
		"start_line": 2,
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\nBETA\ngamma\n"
	if string(data) != want {
		t.Fatalf("content mismatch:\ngot:  %q\nwant: %q", string(data), want)
	}
}

func TestEditMultipleMatchesRequiresReplaceAll(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "e.txt")
	if err := os.WriteFile(p, []byte("foo\nfoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "foo",
		"new_string": "bar",
	})
	if !res.IsError {
		t.Fatal("expected error for multiple matches without replace_all")
	}
	if !strings.Contains(res.Output, "replace_all=true") {
		t.Fatalf("expected replace_all hint, got: %s", res.Output)
	}

	res = NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":   p,
		"old_string":  "foo",
		"new_string":  "bar",
		"replace_all": true,
	})
	if res.IsError {
		t.Fatalf("replace_all edit failed: %s", res.Output)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bar\nbar\n" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestEditOldStringNotFoundHints(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(p, []byte("abc\ndef\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "xyz",
		"new_string": "123",
	})
	if !res.IsError {
		t.Fatal("expected error for missing old_string")
	}
	if res.Output != "old_string not found" {
		t.Fatalf("unexpected output: %s", res.Output)
	}
	if !strings.Contains(res.HintsForModel, "Read output") {
		t.Fatalf("expected model hint about Read output, got: %s", res.HintsForModel)
	}
}

func TestEditLineRangeOutOfRange(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "g.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "",
		"new_string": "x",
		"start_line": 10,
	})
	if !res.IsError {
		t.Fatal("expected error for out-of-range start_line")
	}
	if !strings.Contains(res.Output, "out of range") {
		t.Fatalf("expected out of range error, got: %s", res.Output)
	}
}
