package slash

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
)

func setupSkillHome(t *testing.T) (home, opencode, devin, custom string) {
	t.Helper()
	home = t.TempDir()
	opencode = filepath.Join(home, ".config", "opencode", "skills")
	devin = filepath.Join(home, ".config", "devin", "skills")
	custom = filepath.Join(home, "mine")
	for _, d := range []string{opencode, devin, custom} {
		if err := os.MkdirAll(filepath.Join(d, "s"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "s", "SKILL.md"), []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = old })
	return home, opencode, devin, custom
}

func TestParseSkillsSetInput(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	custom := t.TempDir()
	empty := t.TempDir()
	rel := filepath.Join(cwd, "relskills")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	options := []skillsOption{
		{Dir: "/opt/one", Tool: "A", Count: 1},
		{Dir: "/opt/two", Tool: "B", Count: 2},
		{Dir: "/opt/three", Tool: "C", Count: 3},
	}
	cfg := &config.Config{Cwd: cwd}
	abs := func(p string) string { return filepath.Clean(p) }
	cases := []struct {
		name    string
		input   string
		want    []string // nil = no change
		wantErr string
	}{
		{"blank", "", nil, ""},
		{"spaces", "   ", nil, ""},
		{"numbers", "1,3", []string{abs("/opt/one"), abs("/opt/three")}, ""},
		{"numbers spaced", "1 2", []string{abs("/opt/one"), abs("/opt/two")}, ""},
		{"all", "all", []string{abs("/opt/one"), abs("/opt/two"), abs("/opt/three")}, ""},
		{"none", "none", []string{}, ""},
		{"none plus number", "none 1", nil, "cannot be combined"},
		{"none plus path", "none /x", nil, "cannot be combined"},
		{"out of range", "4", nil, "out of range"},
		{"zero", "0", nil, "out of range"},
		{"bad path", "/definitely/missing/dir", nil, "not an existing directory"},
		{"abs path", custom, []string{abs(custom)}, ""},
		{"rel path", "relskills", []string{abs(rel)}, ""},
		{"home path", "~/x", nil, "not an existing directory"},
		{"dedupe", "1 1 " + custom + " " + custom, []string{abs("/opt/one"), abs(custom)}, ""},
		{"order", "3 " + custom + " 1", []string{abs("/opt/three"), abs("/opt/one"), abs(custom)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := parseSkillsSetInput(tc.input, options, cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected %q error, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}

	// A directory with no skills is accepted but warns.
	got, warnings, err := parseSkillsSetInput(empty, options, cfg)
	if err != nil || len(got) != 1 {
		t.Fatalf("expected the empty dir accepted, got %v (%v)", got, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no skills found") {
		t.Fatalf("expected a no-skills warning, got %v", warnings)
	}
}

func TestSkillsSetWithArgs(t *testing.T) {
	_, opencode, devin, _ := setupSkillHome(t)
	var out bytes.Buffer
	ctx := &Ctx{Cfg: &config.Config{Cwd: t.TempDir()}, Out: &out}

	if err := runSkillsCommand(ctx, []string{"set", "2"}); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Cfg.SkillsDirs) != 1 || ctx.Cfg.SkillsDirs[0] != devin {
		t.Fatalf("expected Devin selected, got %v", ctx.Cfg.SkillsDirs)
	}
	if !strings.Contains(out.String(), "Skill folders updated (1)") || !strings.Contains(out.String(), "~/.config/devin/skills") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	if strings.Contains(out.String(), opencode) && strings.Contains(out.String(), "[x] 1") {
		t.Fatal("opencode should not be marked selected")
	}
	_ = opencode
}

func TestSkillsSetInteractiveAndNone(t *testing.T) {
	setupSkillHome(t)
	var out bytes.Buffer
	ctx := &Ctx{Cfg: &config.Config{Cwd: t.TempDir()}, Out: &out}
	ctx.Prompter = &fakePrompter{answers: []string{"all"}}

	if err := runSkillsCommand(ctx, []string{"set"}); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Cfg.SkillsDirs) != 2 {
		t.Fatalf("expected both detected dirs, got %v", ctx.Cfg.SkillsDirs)
	}
	if !strings.Contains(out.String(), "Skill folders updated (2)") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}

	ctx.Prompter = &fakePrompter{answers: []string{"none"}}
	out.Reset()
	if err := runSkillsCommand(ctx, []string{"set"}); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Cfg.SkillsDirs) != 0 || !strings.Contains(out.String(), "cleared") {
		t.Fatalf("expected cleared dirs, got %v / %s", ctx.Cfg.SkillsDirs, out.String())
	}
}

func TestSkillsSetNoPrompterPrintsUsage(t *testing.T) {
	setupSkillHome(t)
	var out bytes.Buffer
	ctx := &Ctx{Cfg: &config.Config{Cwd: t.TempDir()}, Out: &out}
	if err := runSkillsCommand(ctx, []string{"set"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Usage: /skills set") {
		t.Fatalf("expected usage without a prompter, got:\n%s", out.String())
	}
}

func TestSkillsListHintsAtDetectedFolders(t *testing.T) {
	setupSkillHome(t)
	var out bytes.Buffer
	ctx := &Ctx{Cfg: &config.Config{Cwd: t.TempDir(), ConfigDir: t.TempDir()}, Out: &out}
	if err := runSkillsCommand(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// With no skills loaded at all, the empty-state message mentions set.
	if !strings.Contains(out.String(), "/skills set") {
		t.Fatalf("expected /skills set mention, got:\n%s", out.String())
	}
}

func TestSkillsListHintWhenLoadedButNoSkillsDirs(t *testing.T) {
	home, _, _, _ := setupSkillHome(t)
	// A project skill exists so the list is non-empty; SkillsDirs stays
	// empty so the detection hint shows.
	cwd := filepath.Join(home, "proj")
	if err := os.MkdirAll(filepath.Join(cwd, ".vibe-coder", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".vibe-coder", "skills", "mine.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ctx := &Ctx{Cfg: &config.Config{Cwd: cwd, ConfigDir: filepath.Join(home, "cfg")}, Out: &out}
	if err := runSkillsCommand(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Found skill folders from other tools") || !strings.Contains(out.String(), "OpenCode") {
		t.Fatalf("expected detection hint, got:\n%s", out.String())
	}
}

func TestDiskKeyChangesWithSkillsDirs(t *testing.T) {
	tmp := t.TempDir()
	extra := filepath.Join(tmp, "extra")
	if err := os.MkdirAll(filepath.Join(extra, "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extra, "s", "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Cwd: t.TempDir(), ConfigDir: t.TempDir()}
	before := skills.DiskKey(cfg)
	cfg.SkillsDirs = []string{extra}
	after := skills.DiskKey(cfg)
	if before == after || !strings.Contains(after, "SKILL.md") {
		t.Fatalf("expected DiskKey to include the new root, before=%q after=%q", before, after)
	}
}
