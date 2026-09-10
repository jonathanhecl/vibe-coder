package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func checkpointRepo(t *testing.T) (string, func(...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init")
	return root, run
}

func TestCheckpointRestoresFileStates(t *testing.T) {
	for _, kind := range []string{"clean", "staged", "untracked", "ignored", "new"} {
		t.Run(kind, func(t *testing.T) {
			root, run := checkpointRepo(t)
			path := filepath.Join(root, "file.txt")
			if kind != "new" {
				writeFixture(t, path, "before")
			}
			if kind == "clean" || kind == "staged" {
				run("add", "file.txt")
				run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
			}
			if kind == "staged" {
				writeFixture(t, path, "staged")
				run("add", "file.txt")
				writeFixture(t, path, "before")
			}
			if kind == "ignored" {
				writeFixture(t, filepath.Join(root, ".gitignore"), "file.txt\n")
			}
			index := run("ls-files", "--stage")
			status := run("status", "--porcelain", "--untracked-files=all")
			cp := NewCheckpoint(root)
			if err := cp.Create("edit", path); err != nil {
				t.Fatal(err)
			}
			if got := run("status", "--porcelain", "--untracked-files=all"); got != status {
				t.Fatalf("checkpoint changed status: %q != %q", got, status)
			}
			writeFixture(t, path, "agent edit")
			if err := cp.Complete(); err != nil {
				t.Fatal(err)
			}
			if err := NewCheckpoint(root).Rollback(); err != nil {
				t.Fatal(err)
			}
			if kind == "new" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("new file was not undone: %v", err)
				}
			} else if got := readFile(t, path); got != "before" {
				t.Fatalf("restored contents: %q", got)
			}
			if got := run("ls-files", "--stage"); got != index {
				t.Fatalf("index changed: %q != %q", got, index)
			}
			if err := cp.Rollback(); !errors.Is(err, ErrNoCheckpoint) {
				t.Fatalf("expected consumed checkpoint, got %v", err)
			}
		})
	}
}

func TestCheckpointConsecutiveEditsAndConflict(t *testing.T) {
	root, _ := checkpointRepo(t)
	path := filepath.Join(root, "file.txt")
	writeFixture(t, path, "original")
	cp := NewCheckpoint(root)
	for _, next := range []string{"first", "second"} {
		if err := cp.Create("edit", path); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, path, next)
		if err := cp.Complete(); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, path, "external change")
	if err := cp.Rollback(); err == nil || !strings.Contains(err.Error(), "file changed") {
		t.Fatalf("expected conflict, got %v", err)
	}
	if got := readFile(t, path); got != "external change" {
		t.Fatalf("external edit overwritten: %q", got)
	}
	writeFixture(t, path, "second")
	for _, want := range []string{"first", "original"} {
		if err := cp.Rollback(); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, path); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestCheckpointDiscardAndNoOpPreserveUndo(t *testing.T) {
	root, _ := checkpointRepo(t)
	path := filepath.Join(root, "file.txt")
	writeFixture(t, path, "original")
	cp := NewCheckpoint(root)
	if err := cp.Create("edit", path); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, "edited")
	if err := cp.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := cp.Create("failed", path); err != nil {
		t.Fatal(err)
	}
	if err := cp.Discard(); err != nil {
		t.Fatal(err)
	}
	if err := cp.Create("unchanged", path); err != nil {
		t.Fatal(err)
	}
	if err := cp.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := cp.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "original" {
		t.Fatalf("previous checkpoint lost: %q", got)
	}
}

func TestCheckpointRejectsUnsafeTargets(t *testing.T) {
	root, _ := checkpointRepo(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFixture(t, outside, "untouched")
	for _, path := range []string{outside, "../outside.txt", filepath.Join(root, ".git", "config"), root} {
		if err := NewCheckpoint(root).Create("unsafe", path); err == nil {
			t.Fatalf("accepted unsafe path %s", path)
		}
	}
	if err := NewCheckpoint(root).Create("missing path"); err == nil {
		t.Fatal("accepted missing checkpoint path")
	}
	if got := readFile(t, outside); got != "untouched" {
		t.Fatalf("outside file changed: %q", got)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := NewCheckpoint(root).Create("symlink", filepath.Join(link, "outside.txt")); err == nil {
		t.Fatal("accepted symlink ancestor")
	}
}

func TestCheckpointPreservesExistingStashes(t *testing.T) {
	root, run := checkpointRepo(t)
	path := filepath.Join(root, "file.txt")
	writeFixture(t, path, "original")
	run("add", ".")
	run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	writeFixture(t, path, "user stash")
	run("stash", "push", "-m", "user checkpoint")
	before := run("stash", "list")
	cp := NewCheckpoint(root)
	if err := cp.Create("edit", path); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, "edited")
	if err := cp.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := cp.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := run("stash", "list"); got != before {
		t.Fatalf("user stashes changed: %q", got)
	}
}

func TestCheckpointRelativePathFromSubdirectory(t *testing.T) {
	root, _ := checkpointRepo(t)
	dir := filepath.Join(root, "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file.txt")
	writeFixture(t, path, "original")
	cp := NewCheckpoint(dir)
	if err := cp.Create("edit", "file.txt"); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, "edited")
	if err := cp.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := cp.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "original" {
		t.Fatalf("wrong relative checkpoint: %q", got)
	}
}

func TestCheckpointRestoresPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	root, _ := checkpointRepo(t)
	path := filepath.Join(root, "run.sh")
	writeFixture(t, path, "original")
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatal(err)
	}
	cp := NewCheckpoint(root)
	if err := cp.Create("edit", path); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, "edited")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cp.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := cp.Rollback(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("mode not restored: %v", info.Mode())
	}
}
