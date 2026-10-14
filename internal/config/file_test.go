package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveModelSettingsPersistsSkillsDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vibe-coder.env")
	initial := "# keep me\n" +
		"SKILLS_DIRS=/old/a\n" +
		"UNRELATED_KEY=keepme\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	dirs := []string{"/new/one", "/new/two"}
	cfg := &Config{ConfigFile: path, SkillsDirs: dirs}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.Contains(content, "# keep me") || !strings.Contains(content, "UNRELATED_KEY=keepme") {
		t.Fatalf("unrelated keys/comments lost:\n%s", content)
	}
	if strings.Contains(content, "/old/a") {
		t.Fatalf("stale SKILLS_DIRS survived:\n%s", content)
	}
	sep := string(os.PathListSeparator)
	parsed := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		parsed[k] = strings.Trim(v, `"`)
	}
	if parsed["SKILLS_DIRS"] != strings.Join(dirs, sep) {
		t.Fatalf("SKILLS_DIRS=%q, want %q", parsed["SKILLS_DIRS"], strings.Join(dirs, sep))
	}

	// Reload through the file parser.
	reloaded := &Config{}
	if err := applyConfigFile(reloaded, path); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.SkillsDirs) != 2 || reloaded.SkillsDirs[0] != dirs[0] || reloaded.SkillsDirs[1] != dirs[1] {
		t.Fatalf("reloaded SkillsDirs=%v", reloaded.SkillsDirs)
	}
}

func TestSaveModelSettingsClearsSkillsDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vibe-coder.env")
	if err := os.WriteFile(path, []byte("SKILLS_DIRS=/old/a\n# stay\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{ConfigFile: path}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SKILLS_DIRS") {
		t.Fatalf("empty SkillsDirs should remove the key:\n%s", raw)
	}
	if !strings.Contains(string(raw), "# stay") {
		t.Fatalf("comment lost:\n%s", raw)
	}
}
