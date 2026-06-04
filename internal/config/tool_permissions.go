package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ToolPermissionsKey is the env-style key used inside vibe-coder.env for saved
// allow/deny rules (JSON object: {"write":"allow",...}).
const ToolPermissionsKey = "TOOL_PERMISSIONS"

// ParseToolPermissionsFromEnvContent extracts the TOOL_PERMISSIONS JSON object
// from a vibe-coder.env-style file body.
func ParseToolPermissionsFromEnvContent(data []byte) (map[string]string, bool) {
	raw := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != ToolPermissionsKey {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		var out map[string]string
		if err := json.Unmarshal([]byte(value), &out); err != nil || out == nil {
			return nil, false
		}
		return out, true
	}
	return nil, false
}

// UpsertToolPermissions writes or replaces the TOOL_PERMISSIONS line in the
// given config file, preserving other lines (same strategy as SaveModelSettings).
func UpsertToolPermissions(path string, perms map[string]string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("empty config path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	lines := []string{}
	if data, err := os.ReadFile(path); err == nil {
		raw := strings.ReplaceAll(string(data), "\r\n", "\n")
		lines = strings.Split(raw, "\n")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config file: %w", err)
	}

	compact, err := json.Marshal(perms)
	if err != nil {
		return fmt.Errorf("marshal permissions: %w", err)
	}
	updateValue := string(compact)
	dropKey := len(perms) == 0 || updateValue == "{}"

	seen := false
	out := make([]string, 0, len(lines)+2)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			out = append(out, line)
			continue
		}
		if strings.TrimSpace(key) != ToolPermissionsKey {
			out = append(out, line)
			continue
		}
		seen = true
		if !dropKey {
			out = append(out, ToolPermissionsKey+"="+updateValue)
		}
	}
	if !seen && !dropKey {
		out = append(out, ToolPermissionsKey+"="+updateValue)
	}

	content := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}
