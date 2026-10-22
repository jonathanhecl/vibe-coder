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

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
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

		if applyProviderKey(cfg, key, value) {
			continue
		}
		if applyRoleKey(cfg, key, value) {
			continue
		}

		switch key {
		case "MODEL":
			cfg.Model = value
		case "UI":
			cfg.UI = value
		case "SIDECAR_MODEL":
			cfg.SidecarModel = value
		case "JEVSTYLE_MODEL", "JEV_STYLE_MODEL", "JEVSTYLE_V3_ENDPOINT", "JEV_STYLE_V3_ENDPOINT":
			cfg.LegacyJevstyle = true
		case "DECISION_MODEL":
			cfg.DecisionModel = value
		case "DECISION_ENDPOINT":
			cfg.DecisionEndpoint = value
		case "DECISION_API_KEY":
			cfg.DecisionAPIKey = value
		case "ASSISTED_YES", "ASSISTED_MODE":
			if b, ok := parseBoolish(value); ok {
				cfg.AssistedYes = b
			}
		case "SIDECAR_DISABLED":
			if b, ok := parseBoolish(value); ok {
				cfg.SidecarDisabled = b
			}
		case "SIDECAR_ENABLED":
			if b, ok := parseBoolish(value); ok {
				cfg.SidecarDisabled = !b
			}
		case "MODEL_ENDPOINT", "ENDPOINT":
			cfg.ModelEndpoint = value
		case "MODEL_API_KEY":
			cfg.ModelApiKey = value
		case "MODEL_PROVIDER", "PROVIDER":
			cfg.ModelProvider = value
		case "SIDECAR_ENDPOINT":
			cfg.SidecarEndpoint = value
		case "SIDECAR_API_KEY":
			cfg.SidecarApiKey = value
		case "SIDECAR_PROVIDER":
			cfg.SidecarProvider = value
		case "OLLAMA_HOST":
			cfg.OllamaHost = value
		case "OLLAMA_API_KEY":
			p := cfg.ensureProviderMap()[DefaultProviderName]
			p.Name = DefaultProviderName
			p.APIKey = value
			cfg.Providers[DefaultProviderName] = p
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
		case "PERMISSION_TIMEOUT":
			if parsed, ok := ParsePermissionTimeout(value); ok {
				cfg.PermissionTimeout = parsed
			}
		case "NO_THINK":
			if b, ok := parseBoolish(value); ok {
				cfg.OllamaNoThink = b
			}
		case "THINK":
			if norm, ok := ollama.NormalizeThinkLevel(value); ok && norm != "" {
				cfg.OllamaThinkLevel = norm
			}
		case "HIDE_THINK", "HIDE_THINKING":
			if b, ok := parseBoolish(value); ok {
				cfg.OllamaHideThink = b
			}
		case "TEMPORAL":
			if b, ok := parseBoolish(value); ok {
				cfg.Temporal = b
			}
		case "ISOLATED":
			if b, ok := parseBoolish(value); ok {
				cfg.Isolated = b
			}
		case "CONTEXT":
			cfg.ContextFiles = append(cfg.ContextFiles, splitPathList(value)...)
		case "SKILLS_DIRS":
			cfg.SkillsDirs = splitPathList(value)
		case "KEEP_TOOL_RESULTS":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.Harness.KeepToolResults = parsed
			}
		case "TOOL_OUTPUT_MAX_TOKENS":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.Harness.ToolOutputMaxTokens = parsed
			}
		case "COMPACT_AT_PERCENT":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.Harness.CompactAtPercent = parsed
			}
		case "ASK_TIMEOUT_MS":
			// Retired: interactive questions share PERMISSION_TIMEOUT (the
			// user-action budget). Ignored so an old config file still loads.
		case "RAG_MODE":
			cfg.RAGMode = value
		case "KEEP_ALIVE_MAIN":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.KeepAliveMain = parsed
			}
		case "KEEP_ALIVE_WORKER":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.KeepAliveWorker = parsed
			}
		case "METRICS_DISABLED":
			if b, ok := parseBoolish(value); ok {
				cfg.Harness.MetricsDisabled = b
			}
		case "LSP_ENABLED":
			cfg.LSP.Enabled = value
		case "LSP_SERVERS":
			cfg.LSP.Servers = parseLSPServers(value)
		case "LSP_AFTER_EDIT":
			if b, ok := parseBoolish(value); ok {
				cfg.LSP.AfterEdit = b
			}
		case "LSP_TIMEOUT_MS":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.LSP.TimeoutMS = parsed
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan config file: %w", err)
	}
	return nil
}

// applyProviderKey parses PROVIDER_<NAME>_KIND|ENDPOINT|API_KEY config keys.
// It reports whether the key belonged to the provider namespace.
func applyProviderKey(cfg *Config, key, value string) bool {
	rest, ok := strings.CutPrefix(key, "PROVIDER_")
	if !ok {
		return false
	}
	suffix := ""
	for _, candidate := range []string{"_ENDPOINT", "_API_KEY", "_KIND"} {
		if strings.HasSuffix(rest, candidate) {
			suffix = candidate
			break
		}
	}
	if suffix == "" {
		return true
	}
	name := NormalizeProviderName(strings.TrimSuffix(rest, suffix))
	if !ValidProviderName(name) {
		return true
	}
	providers := cfg.ensureProviderMap()
	p := providers[name]
	p.Name = name
	switch suffix {
	case "_ENDPOINT":
		p.Endpoint = value
		if p.Kind == "" {
			p.Kind = KindForEndpoint(value)
		}
		if name == DefaultProviderName {
			cfg.OllamaHost = value
		}
	case "_API_KEY":
		p.APIKey = value
	case "_KIND":
		kind, ok := ParseProviderKind(value)
		if !ok {
			return true
		}
		p.Kind = kind
	}
	providers[name] = p
	return true
}

// applyRoleKey parses ROLE_<ROLE>_PROVIDER|MODEL config keys.
// It reports whether the key belonged to the role namespace.
func applyRoleKey(cfg *Config, key, value string) bool {
	rest, ok := strings.CutPrefix(key, "ROLE_")
	if !ok {
		return false
	}
	suffix := ""
	for _, candidate := range []string{"_PROVIDER", "_MODEL"} {
		if strings.HasSuffix(rest, candidate) {
			suffix = candidate
			break
		}
	}
	if suffix == "" {
		return true
	}
	role, ok := ParseRole(strings.TrimSuffix(rest, suffix))
	if !ok {
		return true
	}
	binding := cfg.ensureRoleMap()[role]
	switch suffix {
	case "_PROVIDER":
		binding.Provider = NormalizeProviderName(value)
	case "_MODEL":
		binding.Model = value
	}
	cfg.RoleModels[role] = binding
	return true
}

// isMigratedKey reports whether a legacy key is now represented by
// PROVIDER_*/ROLE_* keys and should be dropped on save.
func isMigratedKey(key string) bool {
	switch key {
	case "MODEL", "SIDECAR_MODEL", "MODEL_ENDPOINT", "MODEL_PROVIDER", "MODEL_API_KEY",
		"SIDECAR_ENDPOINT", "SIDECAR_PROVIDER", "SIDECAR_API_KEY",
		"DECISION_MODEL", "DECISION_ENDPOINT", "DECISION_API_KEY",
		"OLLAMA_HOST", "OLLAMA_API_KEY", "ENDPOINT", "PROVIDER":
		return true
	}
	return false
}

// persistEntries returns the config keys to write and their canonical order.
func (c *Config) persistEntries() (map[string]string, []string) {
	updates := map[string]string{
		"ASSISTED_YES":       strconv.FormatBool(c.AssistedYes),
		"HIDE_THINK":         strconv.FormatBool(c.OllamaHideThink),
		"THINK":              strings.TrimSpace(c.OllamaThinkLevel),
		"KEEP_ALIVE_MAIN":    strconv.Itoa(c.KeepAliveMain),
		"KEEP_ALIVE_WORKER":  strconv.Itoa(c.KeepAliveWorker),
		"SKILLS_DIRS":        strings.Join(c.SkillsDirs, string(os.PathListSeparator)),
		"PERMISSION_TIMEOUT": formatPermissionTimeoutSetting(c.PermissionTimeout),
	}
	ordered := []string{"HIDE_THINK", "THINK", "KEEP_ALIVE_MAIN", "KEEP_ALIVE_WORKER", "SKILLS_DIRS", "PERMISSION_TIMEOUT"}

	for _, name := range c.ProviderNames() {
		p := c.Providers[name]
		upper := strings.ToUpper(name)
		kindKey := "PROVIDER_" + upper + "_KIND"
		endpointKey := "PROVIDER_" + upper + "_ENDPOINT"
		keyKey := "PROVIDER_" + upper + "_API_KEY"
		kind := p.Kind
		if kind == "" {
			kind = KindForEndpoint(p.Endpoint)
		}
		updates[kindKey] = string(kind)
		updates[endpointKey] = strings.TrimSpace(p.Endpoint)
		updates[keyKey] = strings.TrimSpace(p.APIKey)
		ordered = append(ordered, kindKey, endpointKey, keyKey)
	}
	for _, role := range AllRoles {
		binding := c.RoleModels[role]
		upper := strings.ToUpper(string(role))
		providerKey := "ROLE_" + upper + "_PROVIDER"
		modelKey := "ROLE_" + upper + "_MODEL"
		updates[providerKey] = strings.TrimSpace(binding.Provider)
		updates[modelKey] = strings.TrimSpace(binding.Model)
		ordered = append(ordered, providerKey, modelKey)
	}
	return updates, ordered
}

// SaveModelSettings persists runtime model settings to the vibe-coder config file.
// It writes the PROVIDER_*/ROLE_* keys, keeps HIDE_THINK/THINK/ASSISTED_YES and
// SIDECAR_DISABLED, and drops the legacy model/endpoint keys it replaces while
// preserving unrelated keys and comments.
func SaveModelSettings(cfg *Config) error {
	path := strings.TrimSpace(cfg.ConfigFile)
	if path == "" {
		path = filepath.Join(cfg.ConfigDir, "vibe-coder.env")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	cfg.adoptLegacyFields()

	lines := []string{}
	if data, err := os.ReadFile(path); err == nil {
		raw := strings.ReplaceAll(string(data), "\r\n", "\n")
		lines = strings.Split(raw, "\n")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config file: %w", err)
	}

	updates, ordered := cfg.persistEntries()

	seen := map[string]bool{}
	out := make([]string, 0, len(lines)+len(ordered))
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
		if key == "SIDECAR_DISABLED" || isMigratedKey(key) {
			continue
		}
		value, shouldUpdate := updates[key]
		if !shouldUpdate {
			if strings.HasPrefix(key, "PROVIDER_") || strings.HasPrefix(key, "ROLE_") {
				continue // stale provider or role key
			}
			out = append(out, line)
			continue
		}
		seen[key] = true
		if value == "" {
			continue
		}
		out = append(out, key+"="+value)
	}
	for _, key := range ordered {
		if seen[key] {
			continue
		}
		if value := updates[key]; value != "" {
			out = append(out, key+"="+value)
		}
	}
	if !seen["ASSISTED_YES"] && cfg.AssistedYes {
		out = append(out, "ASSISTED_YES=true")
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
