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
	vr := validateExistingFileForRead(missing)
	if !vr.IsError() {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(vr.UserError, "PATH ERROR") {
		t.Fatalf("expected PATH ERROR prefix, got: %s", vr.UserError)
	}
	if !strings.Contains(vr.AssistantHints, "For the assistant") {
		t.Fatalf("expected assistant hints: %s", vr.AssistantHints)
	}
}

func TestValidateExistingFileForRead_notAbs(t *testing.T) {
	t.Parallel()
	vr := validateExistingFileForRead("relative.txt")
	if !vr.IsError() || !strings.Contains(vr.UserError, "absolute") {
		t.Fatalf("got userError: %s, hints: %s", vr.UserError, vr.AssistantHints)
	}
}

func TestValidateExistingFileForRead_schemeInPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bad := tmp + string(filepath.Separator) + `bad://still/missing.txt`
	vr := validateExistingFileForRead(bad)
	if !vr.IsError() {
		t.Fatal("expected error")
	}
	if !strings.Contains(vr.AssistantHints, "://") {
		t.Fatalf("expected hint about :// schemes, got userError: %s, hints: %s", vr.UserError, vr.AssistantHints)
	}
}

func TestValidateWriteTargetPath_newFileOK(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	newFile := filepath.Join(tmp, "newdir", "out.txt")
	vr := validateWriteTargetPath(newFile)
	if vr.IsError() {
		t.Fatalf("unexpected: %s", vr.UserError)
	}
}

func TestValidateExistingFileForRead_ok(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	f := filepath.Join(tmp, "a.go")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	vr := validateExistingFileForRead(f)
	if vr.IsError() {
		t.Fatalf("unexpected: %s", vr.UserError)
	}
}
