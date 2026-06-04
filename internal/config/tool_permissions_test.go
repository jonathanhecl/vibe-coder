package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndUpsertToolPermissions(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "vibe-coder.env")
	body := "MODEL=qwen\n# comment\nTOOL_PERMISSIONS={\"write\":\"allow\",\"edit\":\"deny\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseToolPermissionsFromEnvContent(data)
	if !ok || got["write"] != "allow" || got["edit"] != "deny" {
		t.Fatalf("parse: ok=%v got=%v", ok, got)
	}
	if err := UpsertToolPermissions(path, map[string]string{"webfetch": "allow"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(after)
	if !strings.Contains(s, "MODEL=qwen") {
		t.Fatal("expected MODEL preserved")
	}
	if !strings.Contains(s, ToolPermissionsKey) || !strings.Contains(s, "webfetch") {
		t.Fatalf("expected updated TOOL_PERMISSIONS: %s", s)
	}
}
