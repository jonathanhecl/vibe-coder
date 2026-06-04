package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func (m *Manager) loadPersistent() {
	if strings.TrimSpace(m.file) == "" {
		return
	}
	info, err := os.Lstat(m.file)
	if err != nil {
		m.tryLoadLegacyPermissionsFile()
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return
	}
	data, err := os.ReadFile(m.file)
	if err != nil {
		m.tryLoadLegacyPermissionsFile()
		return
	}
	if p, ok := config.ParseToolPermissionsFromEnvContent(data); ok {
		m.mergePersistentMap(p)
	} else if raw := parseLegacyJSONPermissionsFile(data); len(raw) > 0 {
		// Standalone JSON file (old layout or misnamed config).
		m.mergePersistentMap(raw)
	}
	if !strings.Contains(string(data), config.ToolPermissionsKey+"=") {
		m.tryLoadLegacyPermissionsFile()
	}
}

func parseLegacyJSONPermissionsFile(data []byte) map[string]string {
	trim := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trim, "{") {
		return nil
	}
	raw := map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return raw
}

func (m *Manager) mergePersistentMap(raw map[string]string) {
	for k, v := range raw {
		k = normalizeTool(k)
		v = strings.ToLower(strings.TrimSpace(v))
		if k == "bash" && v == "allow" {
			continue
		}
		if v != "allow" && v != "deny" {
			continue
		}
		m.persistent[k] = v
	}
}

// tryLoadLegacyPermissionsFile loads permissions.json next to vibe-coder.env once,
// so existing installs keep their rules until the next save (which writes TOOL_PERMISSIONS).
func (m *Manager) tryLoadLegacyPermissionsFile() {
	if strings.TrimSpace(m.file) == "" {
		return
	}
	legacy := filepath.Join(filepath.Dir(m.file), legacyPermissionsFile)
	info, err := os.Lstat(legacy)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		return
	}
	raw := map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	if len(raw) == 0 {
		return
	}
	m.mergePersistentMap(raw)
}

func (m *Manager) savePersistentLocked() error {
	if strings.TrimSpace(m.file) == "" {
		return nil
	}
	out := map[string]string{}
	for k, v := range m.persistent {
		if k == "bash" && v == "allow" {
			continue
		}
		out[k] = v
	}
	if err := config.UpsertToolPermissions(m.file, out); err != nil {
		return err
	}
	// One-time migration: stop using a separate permissions.json next to config.
	legacy := filepath.Join(filepath.Dir(m.file), legacyPermissionsFile)
	if st, err := os.Lstat(legacy); err == nil && !st.IsDir() {
		_ = os.Remove(legacy)
	}
	return nil
}
