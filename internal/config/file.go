package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func configFileExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat config file %q: %w", path, err)
}

func applyConfigFile(cfg *Config, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config file metadata: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink config file: %s", path)
	}
	if info.Size() > maxConfigFileSize {
		return fmt.Errorf("config file too large: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)

		switch key {
		case "MODEL":
			cfg.Model = value
		case "UI":
			cfg.UI = value
		case "SIDECAR_MODEL":
			cfg.SidecarModel = value
		case "SIDECAR_DISABLED":
			if b, ok := parseBoolish(value); ok {
				cfg.SidecarDisabled = b
			}
		case "SIDECAR_ENABLED":
			if b, ok := parseBoolish(value); ok {
				cfg.SidecarDisabled = !b
			}
		case "OLLAMA_HOST":
			cfg.OllamaHost = value
		case "MAX_TOKENS":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.MaxTokens = parsed
			}
		case "TEMPERATURE":
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				cfg.Temperature = parsed
			}
		case "CONTEXT_WINDOW":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.ContextWindow = parsed
			}
		case "CHAT_TIMEOUT":
			if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
				cfg.ChatTimeout = parsed
			}
		case "NO_THINK":
			if b, ok := parseBoolish(value); ok {
				cfg.OllamaNoThink = b
			}
		case "HIDE_THINK", "HIDE_THINKING":
			if b, ok := parseBoolish(value); ok {
				cfg.OllamaHideThink = b
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan config file: %w", err)
	}
	return nil
}

// SaveModelSettings persists runtime model settings to the vibe-coder config file.
// It updates MODEL, SIDECAR_MODEL, OLLAMA_HOST, and SIDECAR_DISABLED while preserving other keys/comments.
func SaveModelSettings(cfg *Config) error {
	path := strings.TrimSpace(cfg.ConfigFile)
	if path == "" {
		path = filepath.Join(cfg.ConfigDir, "vibe-coder.env")
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

	updates := map[string]string{
		"MODEL":         strings.TrimSpace(cfg.Model),
		"SIDECAR_MODEL": strings.TrimSpace(cfg.SidecarModel),
		"OLLAMA_HOST":   strings.TrimSpace(cfg.OllamaHost),
		"HIDE_THINK":    strconv.FormatBool(cfg.OllamaHideThink),
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(lines)+4)
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
		key = strings.TrimSpace(key)
		if key == "SIDECAR_DISABLED" {
			continue
		}
		value, shouldUpdate := updates[key]
		if !shouldUpdate {
			out = append(out, line)
			continue
		}
		seen[key] = true
		if value == "" {
			continue
		}
		out = append(out, key+"="+value)
	}
	for _, key := range []string{"MODEL", "SIDECAR_MODEL", "OLLAMA_HOST", "HIDE_THINK"} {
		if seen[key] {
			continue
		}
		if value := updates[key]; value != "" {
			out = append(out, key+"="+value)
		}
	}
	if cfg.SidecarDisabled {
		out = append(out, "SIDECAR_DISABLED=true")
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
