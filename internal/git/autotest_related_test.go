package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func relatedLayout(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	writeFixture(t, filepath.Join(tmp, "src", "auth.py"), "x = 1\n")
	writeFixture(t, filepath.Join(tmp, "tests", "test_auth.py"), "def test_x(): pass\n")
	writeFixture(t, filepath.Join(tmp, "src", "orphan.py"), "y = 2\n")
	writeFixture(t, filepath.Join(tmp, "web", "app.ts"), "export const a = 1;\n")
	writeFixture(t, filepath.Join(tmp, "web", "app.test.ts"), "test('a', () => {});\n")
	writeFixture(t, filepath.Join(tmp, "pkg", "foo.go"), "package foo\n")
	writeFixture(t, filepath.Join(tmp, "pkg", "foo_test.go"),
		"package foo\nimport \"testing\"\nfunc TestFoo(t *testing.T) {}\nfunc TestBar(t *testing.T) {}\nfunc helper() {}\n")
	return tmp
}

func TestSourceResolvesToRelatedTests(t *testing.T) {
	t.Parallel()
	tmp := relatedLayout(t)
	a := NewAutoTest(tmp)

	got := a.buildAutoTestCommand("pytest", filepath.Join(tmp, "src", "auth.py"))
	want := []string{"pytest", "-x", "--no-header", filepath.Join(tmp, "tests", "test_auth.py")}
	if len(got) != len(want) {
		t.Fatalf("pytest related: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pytest related[%d]: got %q want %q", i, got[i], want[i])
		}
	}

	if got := a.buildAutoTestCommand("pytest", filepath.Join(tmp, "src", "orphan.py")); got != nil {
		t.Fatalf("pytest orphan should skip, got %v", got)
	}

	got = a.buildAutoTestCommand("vitest", filepath.Join(tmp, "web", "app.ts"))
	if len(got) != 7 || got[0] != "npm" || got[len(got)-1] != filepath.Join(tmp, "web", "app.test.ts") {
		t.Fatalf("vitest related: got %v", got)
	}

	got = a.buildAutoTestCommand("go", filepath.Join(tmp, "pkg", "foo.go"))
	if len(got) != 3 || got[0] != "go" || got[1] != "test" || got[2] != "./pkg" {
		t.Fatalf("go source package: got %v", got)
	}
}

func TestGoTestFileRunsOnlyItsTests(t *testing.T) {
	t.Parallel()
	tmp := relatedLayout(t)
	a := NewAutoTest(tmp)

	got := a.buildAutoTestCommand("go", filepath.Join(tmp, "pkg", "foo_test.go"))
	if len(got) != 5 || got[0] != "go" || got[1] != "test" || got[2] != "-run" || got[4] != "./pkg" {
		t.Fatalf("go -run command: got %v", got)
	}
	if got[3] != "^(TestFoo|TestBar)$" {
		t.Fatalf("expected -run filter for both tests, got %q", got[3])
	}
}

func TestMissingFileSkips(t *testing.T) {
	t.Parallel()
	tmp := relatedLayout(t)
	a := NewAutoTest(tmp)

	for _, tc := range []struct{ runner, file string }{
		{"pytest", filepath.Join(tmp, "src", "ghost.py")},
		{"vitest", filepath.Join(tmp, "web", "ghost.ts")},
		{"go", filepath.Join(tmp, "pkg", "ghost.go")},
	} {
		if got := a.buildAutoTestCommand(tc.runner, tc.file); got != nil {
			t.Fatalf("%s missing file should skip, got %v", tc.runner, got)
		}
	}
}

func TestParseGoTestFuncs(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "x_test.go")
	writeFixture(t, path, "package x\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\nfunc notATest() {}\n")
	got := parseGoTestFuncs(path)
	if len(got) != 2 || got[0] != "TestOne" || got[1] != "TestTwo" {
		t.Fatalf("unexpected parsed names: %v", got)
	}
	if parseGoTestFuncs(filepath.Join(tmp, "missing_test.go")) != nil {
		t.Fatal("expected nil for missing file")
	}
}

func TestPytestMirrorResolution(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeFixture(t, filepath.Join(tmp, "pkg", "sub", "mod.py"), "x=1\n")
	writeFixture(t, filepath.Join(tmp, "tests", "pkg", "sub", "test_mod.py"), "def test_m(): pass\n")
	a := NewAutoTest(tmp)

	got := a.buildAutoTestCommand("pytest", filepath.Join(tmp, "pkg", "sub", "mod.py"))
	if len(got) != 4 || !strings.HasSuffix(got[3], filepath.Join("tests", "pkg", "sub", "test_mod.py")) {
		t.Fatalf("pytest mirror: got %v", got)
	}
}
