package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func writeSkillMD(t *testing.T, dir, name, frontmatter, body string) string {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	content := body
	if frontmatter != "" {
		content = "---\n" + frontmatter + "\n---\n" + body
	}
	path := filepath.Join(d, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFrontmatterParser(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content, wantName, wantDesc string
	}{
		{"plain", "---\nname: foo\ndescription: does things\n---\nbody", "foo", "does things"},
		{"double quoted", "---\nname: \"foo bar\"\ndescription: \"does things\"\n---\nbody", "foo bar", "does things"},
		{"single quoted", "---\nname: 'foo'\ndescription: 'does things'\n---\nbody", "foo", "does things"},
		{"folded", "---\nname: foo\ndescription: >\n  first line\n  second line\n---\nbody", "foo", "first line second line"},
		{"literal", "---\nname: foo\ndescription: |\n  first line\n  second line\n---\nbody", "foo", "first line\nsecond line"},
		{"extra keys ignored", "---\nname: foo\nlicense: MIT\ndescription: x\nmetadata:\n  a: b\n---\nbody", "foo", "x"},
		{"no frontmatter", "just body text", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm := parseFrontmatter(tc.content)
			if fm["name"] != tc.wantName || fm["description"] != tc.wantDesc {
				t.Fatalf("got %v, want name=%q desc=%q", fm, tc.wantName, tc.wantDesc)
			}
			if body := stripFrontmatter(tc.content); strings.Contains(body, "frontmatter") && tc.name != "no frontmatter" {
				_ = body
			}
		})
	}
	if stripFrontmatter("---\nname: x\n---\nbody here") != "body here" {
		t.Fatal("expected body without frontmatter")
	}
}

func TestDescriptionFallbackFromBody(t *testing.T) {
	t.Parallel()
	desc := descriptionFromBody("# Heading\n\n  First line   of   prose\nsecond")
	if desc != "First line of prose" {
		t.Fatalf("unexpected fallback description: %q", desc)
	}
	long := strings.Repeat("x", 300)
	if got := descriptionFromBody(long); len([]rune(got)) > maxDescriptionRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("expected 200-rune cap with ellipsis, got %d runes", len([]rune(got)))
	}
	if descriptionFromBody("# only a heading\n") != "" {
		t.Fatal("expected empty description")
	}
}

func TestLoadDirFormatAndPrecedence(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cfg")
	cwd := filepath.Join(tmp, "proj")
	extra := filepath.Join(tmp, "extra")

	// Project dir-format skill beats the same-named user and extra skills.
	writeSkillMD(t, filepath.Join(cwd, ".vibe-coder", "skills"), "shared", "name: shared\ndescription: project wins", "body")
	writeSkillMD(t, filepath.Join(cfgDir, "skills"), "shared", "name: shared\ndescription: user loses", "body")
	writeSkillMD(t, extra, "shared", "name: shared\ndescription: extra loses", "body")
	writeSkillMD(t, extra, "extra-only", "name: extra-only\ndescription: from skills_dirs", "body")

	// Subdirs without SKILL.md are skipped; hidden dirs and files too.
	if err := os.MkdirAll(filepath.Join(extra, "_shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillMD(t, extra, ".hidden", "", "hidden")
	if err := os.WriteFile(filepath.Join(extra, ".hidden-flat.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{ConfigDir: cfgDir, Cwd: cwd, SkillsDirs: []string{extra}}
	items := Load(cfg)
	byName := map[string]Skill{}
	for _, s := range items {
		byName[s.Name] = s
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 skills, got %v", items)
	}
	if byName["shared"].Source != "project" || byName["shared"].Description != "project wins" {
		t.Fatalf("expected project precedence, got %+v", byName["shared"])
	}
	if byName["extra-only"].Source != "skills_dirs" {
		t.Fatalf("expected skills_dirs source, got %+v", byName["extra-only"])
	}
	if byName["extra-only"].Dir != filepath.Join(extra, "extra-only") {
		t.Fatalf("expected skill base dir, got %q", byName["extra-only"].Dir)
	}
}

func TestLoadEarlierSkillsDirsWins(t *testing.T) {
	tmp := t.TempDir()
	first := filepath.Join(tmp, "first")
	second := filepath.Join(tmp, "second")
	writeSkillMD(t, first, "dup", "", "first body")
	writeSkillMD(t, second, "dup", "description: second loses", "second body")
	cfg := &config.Config{Cwd: t.TempDir(), ConfigDir: t.TempDir(), SkillsDirs: []string{first, second}}
	items := Load(cfg)
	if len(items) != 1 || items[0].Path != filepath.Join(first, "dup", "SKILL.md") {
		t.Fatalf("expected the first SKILLS_DIRS entry to win, got %+v", items)
	}
}

func TestLoadFlatLegacyAndSymlinkRules(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "proj")
	flat := filepath.Join(cwd, "skills")
	if err := os.MkdirAll(flat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flat, "legacy.md"), []byte("---\ndescription: flat\n---\n<invoke>bad</invoke> body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlinked flat file is rejected.
	if err := os.Symlink(filepath.Join(flat, "legacy.md"), filepath.Join(flat, "link.md")); err != nil {
		t.Fatal(err)
	}
	// A symlinked skill dir is followed.
	real := filepath.Join(tmp, "real-skill")
	writeSkillMD(t, tmp, "real-skill", "name: real\ndescription: via symlink", "body")
	if err := os.Symlink(real, filepath.Join(flat, "linked-skill")); err != nil {
		t.Fatal(err)
	}
	// A SKILL.md that is itself a symlink is rejected.
	target := writeSkillMD(t, tmp, "manifest-target", "name: mf", "body")
	dir := filepath.Join(flat, "badlink")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	items := Load(&config.Config{ConfigDir: t.TempDir(), Cwd: cwd})
	byName := map[string]Skill{}
	for _, s := range items {
		byName[s.Name] = s
	}
	if byName["legacy"].Description != "flat" || byName["legacy"].Dir != "" {
		t.Fatalf("unexpected flat skill: %+v", byName["legacy"])
	}
	if _, ok := byName["link"]; ok {
		t.Fatal("symlinked flat file must be skipped")
	}
	if byName["real"].Dir == "" {
		t.Fatal("symlinked skill dir must be followed")
	}
	if _, ok := byName["mf"]; ok {
		t.Fatal("symlinked SKILL.md must be rejected")
	}
}

func TestReadReturnsBodyAndBundledFiles(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "skills", "demo")
	writeSkillMD(t, filepath.Join(tmp, "skills"), "demo", "name: demo\ndescription: d", "# Demo\n\nDo the thing.\n")
	files := []string{
		"references/ref.md", "scripts/run.sh", "deep/a/b/c.md",
		".hidden/file.txt", // hidden dir → skipped
	}
	for _, f := range files[:3] {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Depth 4 file must not appear (maxBundledDepth = 3).
	p := filepath.Join(dir, "d1", "d2", "d3", "d4", "too-deep.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	items := Load(&config.Config{ConfigDir: t.TempDir(), Cwd: t.TempDir(), SkillsDirs: []string{filepath.Join(tmp, "skills")}})
	if len(items) != 1 {
		t.Fatalf("expected 1 skill, got %v", items)
	}
	body, bundled, err := Read(items[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "description: d") || !strings.Contains(body, "Do the thing.") {
		t.Fatalf("expected sanitized body without frontmatter, got %q", body)
	}
	joined := strings.Join(bundled, "\n")
	for _, want := range []string{"references", "ref.md", "scripts", "run.sh"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected bundled file %q in %v", want, bundled)
		}
	}
	if strings.Contains(joined, "too-deep") || strings.Contains(joined, "hidden") {
		t.Fatalf("hidden/deep files must be excluded: %v", bundled)
	}
	if strings.Contains(joined, "SKILL.md") {
		t.Fatal("SKILL.md itself must not be a bundled file")
	}
}

func TestRenderCatalog(t *testing.T) {
	t.Parallel()
	if got := RenderCatalog(nil); got != "" {
		t.Fatalf("expected empty catalog, got %q", got)
	}
	items := []Skill{{Name: "a", Description: "alpha"}, {Name: "b", Description: "beta"}}
	got := RenderCatalog(items)
	if !strings.Contains(got, "# Skills") || !strings.Contains(got, "- a: alpha") || !strings.Contains(got, "call the Skill tool") {
		t.Fatalf("unexpected catalog: %q", got)
	}
	many := make([]Skill, 0, 90)
	for i := 0; i < 90; i++ {
		many = append(many, Skill{Name: fmt.Sprintf("s%02d", i), Description: "d"})
	}
	got = RenderCatalog(many)
	if !strings.Contains(got, "and 10 more") {
		t.Fatalf("expected catalog cap note, got tail: %q", got[len(got)-200:])
	}
}

func TestDiskKeyCoversSkillsDirs(t *testing.T) {
	tmp := t.TempDir()
	extra := filepath.Join(tmp, "extra")
	cfg := &config.Config{ConfigDir: t.TempDir(), Cwd: t.TempDir(), SkillsDirs: []string{extra}}
	manifest := writeSkillMD(t, extra, "s1", "", "body")

	before := DiskKey(cfg)
	if !strings.Contains(before, manifest) {
		t.Fatalf("expected the SKILL.md manifest in the disk key, got %q", before)
	}
	// Bump the mtime; the fingerprint must change.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(manifest, future, future); err != nil {
		t.Fatal(err)
	}
	if DiskKey(cfg) == before {
		t.Fatal("expected DiskKey to change with the SKILL.md mtime")
	}
}
