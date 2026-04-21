package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateExistingFileForRead_missing(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "nope.txt")
	msg := validateExistingFileForRead(missing)
	if msg == "" {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(msg, "PATH ERROR") {
		t.Fatalf("expected PATH ERROR prefix, got: %s", msg)
	}
	if !strings.Contains(msg, "For the assistant") {
		t.Fatalf("expected assistant hints: %s", msg)
	}
}

func TestValidateExistingFileForRead_notAbs(t *testing.T) {
	t.Parallel()
	msg := validateExistingFileForRead("relative.txt")
	if msg == "" || !strings.Contains(msg, "absolute") {
		t.Fatalf("got: %s", msg)
	}
}

func TestValidateExistingFileForRead_schemeInPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bad := tmp + string(filepath.Separator) + `bad://still/missing.txt`
	msg := validateExistingFileForRead(bad)
	if msg == "" {
		t.Fatal("expected error")
	}
	if !strings.Contains(msg, "://") {
		t.Fatalf("expected hint about :// schemes, got: %s", msg)
	}
}

func TestValidateWriteTargetPath_newFileOK(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	newFile := filepath.Join(tmp, "newdir", "out.txt")
	msg := validateWriteTargetPath(newFile)
	if msg != "" {
		t.Fatalf("unexpected: %s", msg)
	}
}

func TestValidateExistingFileForRead_ok(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	f := filepath.Join(tmp, "a.go")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := validateExistingFileForRead(f); msg != "" {
		t.Fatalf("unexpected: %s", msg)
	}
}
