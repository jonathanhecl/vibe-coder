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

func TestEditCRLFMultipleMatchesRequiresReplaceAll(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "crlf_multi.txt")
	if err := os.WriteFile(p, []byte("echo 1\r\necho 1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "echo 1",
		"new_string": "echo 2",
	})
	if !res.IsError {
		t.Fatal("expected error for multiple CRLF matches without replace_all")
	}
	if !strings.Contains(res.Output, "replace_all=true") {
		t.Fatalf("expected replace_all hint, got: %s", res.Output)
	}

	res = NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":   p,
		"old_string":  "echo 1",
		"new_string":  "echo 2",
		"replace_all": true,
	})
	if res.IsError {
		t.Fatalf("replace_all CRLF edit failed: %s", res.Output)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "echo 2\r\necho 2\r\n" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestEditStripsReadLineNumberPrefixes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "read_prefix.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc hello() {\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "     3|func hello() {\n     4|\treturn\n     5|}",
		"new_string": "     3|func hello() {\n     4|\tprintln(\"hi\")\n     5|}",
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\n\nfunc hello() {\n\tprintln(\"hi\")\n}\n"
	if string(data) != want {
		t.Fatalf("content mismatch:\ngot:  %q\nwant: %q", string(data), want)
	}
}

func TestEditLineTrimmedReindentsNewString(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "indent.go")
	orig := "func run() {\n\tif ready {\n\t\twork(1)\n\t}\n}\n"
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	// Small model omits outer tab and uses 4 spaces for inner indentation.
	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "if ready {\n    work(1)\n}",
		"new_string": "if ready {\n    work(2)\n}",
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "func run() {\n\tif ready {\n\t\twork(2)\n\t}\n}\n"
	if string(data) != want {
		t.Fatalf("content mismatch:\ngot:  %q\nwant: %q", string(data), want)
	}
}

func TestEditHybridStartLineDisambiguatesWithoutWipingRange(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "hybrid.go")
	orig := strings.Join([]string{
		"func first() error {",
		"\treturn nil",
		"}",
		"func middle() {",
		"\tkeepMe := true",
		"\t_ = keepMe",
		"}",
		"func second() error {",
		"\tval := compute()",
		"\treturn nil",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	// Model provides both a bounding line range (8..11) and an old_string that
	// appears twice in the file. It must replace only line 10, keeping lines 8-9 & 11.
	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"start_line": 8,
		"end_line":   11,
		"old_string": "\treturn nil",
		"new_string": "\treturn err",
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "func first() error {\n\treturn nil\n}") {
		t.Fatalf("first occurrence should stay unchanged:\n%s", got)
	}
	if !strings.Contains(got, "func second() error {\n\tval := compute()\n\treturn err\n}") {
		t.Fatalf("second occurrence should be edited without wiping range:\n%s", got)
	}
}

func TestEditFuzzyBlockMatchAndSafety(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "fuzzy.go")
	orig := strings.Join([]string{
		"func processItems(items []string) int {",
		"\tcount := 0",
		"\tfor _, item := range items {",
		"\t\tif item != \"\" {",
		"\t\t\tcount++",
		"\t\t}",
		"\t}",
		"\treturn count",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	// Single-line typo must NOT fuzzy-match (safety guard).
	resSingle := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "return cont",
		"new_string": "return count + 1",
	})
	if !resSingle.IsError {
		t.Fatal("single-line typo must not fuzzy match")
	}

	// Multi-line block with a minor typo inside line 2 succeeds via high-confidence fuzzy match.
	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path": p,
		"old_string": strings.Join([]string{
			"for _, item := range items {",
			"    if item != \" \" {",
			"        count++",
			"    }",
			"}",
		}, "\n"),
		"new_string": strings.Join([]string{
			"for _, item := range items {",
			"    if strings.TrimSpace(item) != \"\" {",
			"        count++",
			"    }",
			"}",
		}, "\n"),
	})
	if res.IsError {
		t.Fatalf("fuzzy block edit failed: %s", res.Output)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\t\tif strings.TrimSpace(item) != \"\" {") {
		t.Fatalf("expected reindented fuzzy replacement, got:\n%s", string(data))
	}
}

func TestEditNotFoundIncludesClosestMatchSnippet(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "hints.go")
	orig := strings.Join([]string{
		"package demo",
		"",
		"func calculateTotalAmount(price int, qty int) int {",
		"\treturn price * qty",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"old_string": "func calculateTotalAmt(price int, count int) int {\n\treturn price * count\n}",
		"new_string": "func calculateTotalAmount(price int, qty int) int {\n\treturn price * qty * 2\n}",
	})
	if !res.IsError {
		t.Fatal("expected error for low-confidence mismatch")
	}
	if res.Output != "old_string not found" {
		t.Fatalf("unexpected output: %q", res.Output)
	}
	if !strings.Contains(res.HintsForModel, "Read output") {
		t.Fatalf("expected base Read output hint, got: %q", res.HintsForModel)
	}
	if !strings.Contains(res.HintsForModel, "Closest region in file (lines 3-5") {
		t.Fatalf("expected closest region hint, got: %q", res.HintsForModel)
	}
	if !strings.Contains(res.HintsForModel, "3|func calculateTotalAmount") {
		t.Fatalf("expected numbered snippet in hint, got: %q", res.HintsForModel)
	}
}

func TestEditSchemaOldStringOptional(t *testing.T) {
	t.Parallel()
	schema := NewEditTool().Schema()
	req, ok := schema.Function.Parameters["required"].([]string)
	if !ok {
		t.Fatalf("expected []string required, got %T", schema.Function.Parameters["required"])
	}
	hasPath, hasNew, hasOld := false, false, false
	for _, r := range req {
		switch r {
		case "file_path":
			hasPath = true
		case "new_string":
			hasNew = true
		case "old_string":
			hasOld = true
		}
	}
	if !hasPath || !hasNew || hasOld {
		t.Fatalf("expected required=[file_path new_string] without old_string, got %v", req)
	}
}
