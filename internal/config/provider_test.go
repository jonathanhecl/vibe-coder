package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testConfig returns a default config rooted in a temporary directory.
func testConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	return defaultConfig(dir, filepath.Join(dir, "config"), filepath.Join(dir, "state"))
}

// clearRoleEnv neutralizes ambient environment variables that would otherwise
// leak into Load through applyEnv.
func clearRoleEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OLLAMA_HOST", "OLLAMA_API_KEY",
		"VIBE_CODER_MODEL_ENDPOINT", "VIBE_CODER_ENDPOINT", "MODEL_ENDPOINT", "ENDPOINT",
		"VIBE_CODER_MODEL_API_KEY", "MODEL_API_KEY", "OPENAI_API_KEY",
		"VIBE_CODER_MODEL_PROVIDER", "MODEL_PROVIDER", "PROVIDER",
		"VIBE_CODER_SIDECAR_ENDPOINT", "SIDECAR_ENDPOINT",
		"VIBE_CODER_SIDECAR_API_KEY", "SIDECAR_API_KEY",
		"VIBE_CODER_SIDECAR_PROVIDER", "SIDECAR_PROVIDER",
		"VIBE_CODER_MODEL", "VIBEGO_MODEL",
		"VIBE_CODER_SIDECAR_MODEL", "VIBEGO_SIDECAR_MODEL",
		"VIBE_CODER_DECISION_MODEL", "DECISION_MODEL",
		"VIBE_CODER_DECISION_ENDPOINT", "DECISION_ENDPOINT",
		"VIBE_CODER_DECISION_API_KEY", "DECISION_API_KEY",
	} {
		t.Setenv(key, "")
	}
}

func writeConfigFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

func configKeys(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	keys := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		keys[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return keys
}

func TestDefaultRegistryHasOnlyOllama(t *testing.T) {
	cfg := testConfig(t)
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected one default provider, got %d", len(cfg.Providers))
	}
	p, ok := cfg.Provider("")
	if !ok {
		t.Fatal("expected the default provider to resolve")
	}
	if p.Name != DefaultProviderName || p.Kind != ProviderOllama || p.Endpoint != defaultOllamaHost {
		t.Fatalf("unexpected default provider: %+v", p)
	}
	if names := cfg.ProviderNames(); len(names) != 1 || names[0] != DefaultProviderName {
		t.Fatalf("unexpected provider names: %v", names)
	}
	if len(cfg.RoleModels) != 0 {
		t.Fatalf("expected no role bindings, got %v", cfg.RoleModels)
	}
}

func TestAddProviderValidatesAndInfersKind(t *testing.T) {
	cfg := testConfig(t)

	if err := cfg.AddProvider(Provider{Name: "Groq", Endpoint: "https://api.groq.com/openai/v1"}); err != nil {
		t.Fatalf("add provider: %v", err)
	}
	p, ok := cfg.Provider("groq")
	if !ok {
		t.Fatal("expected groq provider")
	}
	if p.Kind != ProviderOpenAI {
		t.Fatalf("expected openai kind, got %q", p.Kind)
	}
	if p.Name != "groq" {
		t.Fatalf("expected a normalized name, got %q", p.Name)
	}

	if err := cfg.AddProvider(Provider{Name: "local", Endpoint: "http://localhost:1234"}); err != nil {
		t.Fatalf("add local provider: %v", err)
	}
	if p, _ := cfg.Provider("local"); p.Kind != ProviderOllama {
		t.Fatalf("expected ollama kind for a non /v1 endpoint, got %q", p.Kind)
	}

	names := cfg.ProviderNames()
	want := []string{DefaultProviderName, "groq", "local"}
	if len(names) != len(want) {
		t.Fatalf("unexpected provider names: %v", names)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("unexpected provider names: %v", names)
		}
	}

	for _, tc := range []struct {
		label    string
		provider Provider
	}{
		{"empty name", Provider{Name: "", Endpoint: "http://x:1"}},
		{"invalid characters", Provider{Name: "bad name!", Endpoint: "http://x:1"}},
		{"default name", Provider{Name: DefaultProviderName, Endpoint: "http://x:1"}},
		{"duplicate", Provider{Name: "groq", Endpoint: "http://x:1"}},
		{"missing endpoint", Provider{Name: "empty", Endpoint: "  "}},
	} {
		if err := cfg.AddProvider(tc.provider); err == nil {
			t.Fatalf("expected an error for %s", tc.label)
		}
	}
}

func TestSetProviderEndpointUpdatesOllamaHost(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.SetProviderEndpoint("", "http://192.168.1.50:11434"); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if cfg.OllamaHost != "http://192.168.1.50:11434" {
		t.Fatalf("expected the ollama host to follow the provider endpoint, got %q", cfg.OllamaHost)
	}
	if p, _ := cfg.Provider(DefaultProviderName); p.Endpoint != "http://192.168.1.50:11434" {
		t.Fatalf("unexpected provider endpoint: %q", p.Endpoint)
	}
	if err := cfg.SetProviderEndpoint("missing", "http://x:1"); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	if err := cfg.SetProviderEndpoint(DefaultProviderName, "  "); err == nil {
		t.Fatal("expected an error for an empty endpoint")
	}
}

func TestSetRoleModelClearsAndRejectsUnknownProvider(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.SetRoleModel(RoleMain, "ghost", "m"); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	if err := cfg.SetRoleModel(Role("nope"), DefaultProviderName, "m"); err == nil {
		t.Fatal("expected an error for an unknown role")
	}
	if err := cfg.SetRoleModel(RoleMain, DefaultProviderName, "qwen3.5:9b"); err != nil {
		t.Fatalf("set main: %v", err)
	}
	if !cfg.RoleInUse(RoleMain) || cfg.Model != "qwen3.5:9b" {
		t.Fatalf("main role not bound: %+v", cfg.RoleModels)
	}
	if err := cfg.SetRoleModel(RoleMain, DefaultProviderName, ""); err != nil {
		t.Fatalf("clear main: %v", err)
	}
	if cfg.RoleInUse(RoleMain) || cfg.Model != "" {
		t.Fatalf("main role should be cleared: %+v", cfg.RoleModels)
	}
	if _, ok := cfg.RoleProvider(RoleMain); ok {
		t.Fatal("expected no provider for a cleared role")
	}
}

func TestRemoveProviderRefusedWhenBound(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.AddProvider(Provider{Name: "groq", Endpoint: "https://api.groq.com/openai/v1"}); err != nil {
		t.Fatalf("add provider: %v", err)
	}
	if err := cfg.SetRoleModel(RoleSidecar, "groq", "llama-3.3-70b"); err != nil {
		t.Fatalf("bind role: %v", err)
	}
	err := cfg.RemoveProvider("groq")
	if err == nil {
		t.Fatal("expected a refusal while the provider is bound")
	}
	if !strings.Contains(err.Error(), string(RoleSidecar)) {
		t.Fatalf("expected the role name in the error, got %q", err)
	}
	if err := cfg.RemoveProvider(DefaultProviderName); err == nil {
		t.Fatal("expected a refusal for the default provider")
	}
	if err := cfg.RemoveProvider("ghost"); err == nil {
		t.Fatal("expected a refusal for an unknown provider")
	}
	cfg.ClearRole(RoleSidecar)
	if err := cfg.RemoveProvider("groq"); err != nil {
		t.Fatalf("remove provider: %v", err)
	}
	if _, ok := cfg.Provider("groq"); ok {
		t.Fatal("provider should be gone")
	}
}

func TestRoleBindingRoundTripThroughSaveAndLoad(t *testing.T) {
	tmp := t.TempDir()
	configFile := filepath.Join(tmp, "vibe-coder.env")
	clearRoleEnv(t)
	t.Setenv("VIBE_CODER_CONFIG", configFile)
	t.Setenv("VIBE_CODER_RAM_GB", "32")
	writeConfigFile(t, configFile, `MODEL="qwen3.5:9b"`)

	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.AddProvider(Provider{Name: "groq", Endpoint: "https://api.groq.com/openai/v1", APIKey: "k1"}); err != nil {
		t.Fatalf("add provider: %v", err)
	}
	if err := cfg.SetRoleModel(RoleMain, "groq", "llama-3.3-70b"); err != nil {
		t.Fatalf("set main: %v", err)
	}
	if err := cfg.SetRoleModel(RoleSidecar, DefaultProviderName, "qwen3.5:4b"); err != nil {
		t.Fatalf("set sidecar: %v", err)
	}
	if err := cfg.SetRoleModel(RoleVision, "groq", "llama-3.2-11b-vision"); err != nil {
		t.Fatalf("set vision: %v", err)
	}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	keys := configKeys(t, configFile)
	if keys["ROLE_MAIN_MODEL"] != "llama-3.3-70b" || keys["ROLE_MAIN_PROVIDER"] != "groq" {
		t.Fatalf("main binding not persisted: %v", keys)
	}
	if keys["ROLE_SIDECAR_MODEL"] != "qwen3.5:4b" {
		t.Fatalf("sidecar binding not persisted: %v", keys)
	}
	if keys["ROLE_VISION_MODEL"] != "llama-3.2-11b-vision" {
		t.Fatalf("vision binding not persisted: %v", keys)
	}
	if keys["PROVIDER_GROQ_ENDPOINT"] != "https://api.groq.com/openai/v1" || keys["PROVIDER_GROQ_API_KEY"] != "k1" {
		t.Fatalf("provider not persisted: %v", keys)
	}
	for _, legacy := range []string{"MODEL", "OLLAMA_HOST", "MODEL_ENDPOINT", "MODEL_API_KEY"} {
		if _, ok := keys[legacy]; ok {
			t.Fatalf("legacy key %s should be dropped on save: %v", legacy, keys)
		}
	}

	reloaded, err := Load([]string{})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := reloaded.RoleModelName(RoleMain); got != "llama-3.3-70b" {
		t.Fatalf("main model after reload: %q", got)
	}
	if got := reloaded.RoleBinding(RoleMain).Provider; got != "groq" {
		t.Fatalf("main provider after reload: %q", got)
	}
	if got := reloaded.RoleModelName(RoleVision); got != "llama-3.2-11b-vision" {
		t.Fatalf("vision model after reload: %q", got)
	}
	if got := reloaded.RoleBinding(RoleSidecar).Provider; got != DefaultProviderName {
		t.Fatalf("sidecar provider after reload: %q", got)
	}
	if !reloaded.IsModelOpenAI() {
		t.Fatal("expected the main endpoint to be treated as OpenAI compatible")
	}
	if got := reloaded.EffectiveModelEndpoint(); got != "https://api.groq.com/openai/v1" {
		t.Fatalf("unexpected effective main endpoint: %q", got)
	}
	if reloaded.ModelApiKey != "k1" {
		t.Fatalf("expected the main api key to survive, got %q", reloaded.ModelApiKey)
	}
}

func TestLegacyFieldsMigrateIntoRegistry(t *testing.T) {
	tmp := t.TempDir()
	configFile := filepath.Join(tmp, "vibe-coder.env")
	clearRoleEnv(t)
	t.Setenv("VIBE_CODER_CONFIG", configFile)
	writeConfigFile(t, configFile,
		`MODEL="legacy-main"`,
		`MODEL_ENDPOINT="https://api.openai.com/v1"`,
		`SIDECAR_MODEL="legacy-sidecar"`,
		`DECISION_ENDPOINT="http://decision-host:8765"`,
	)

	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	main := cfg.RoleBinding(RoleMain)
	if main.Model != "legacy-main" || main.Provider == DefaultProviderName {
		t.Fatalf("main role not migrated: %+v", main)
	}
	p, ok := cfg.Provider(main.Provider)
	if !ok {
		t.Fatalf("expected provider %q to exist", main.Provider)
	}
	if p.Kind != ProviderOpenAI || p.Endpoint != "https://api.openai.com/v1" {
		t.Fatalf("unexpected migrated provider: %+v", p)
	}
	if got := cfg.RoleModelName(RoleSidecar); got != "legacy-sidecar" {
		t.Fatalf("sidecar role not migrated: %q", got)
	}
	decision := cfg.RoleBinding(RoleDecision)
	if decision.Provider == "" || decision.Provider == DefaultProviderName {
		t.Fatalf("decision provider not migrated: %+v", decision)
	}
	if dp, _ := cfg.Provider(decision.Provider); dp.Endpoint != "http://decision-host:8765" {
		t.Fatalf("unexpected decision provider: %+v", dp)
	}
	if !cfg.DecisionInUse() {
		t.Fatal("expected the decision role to be in use from the endpoint alone")
	}
	if cfg.Model != "legacy-main" || cfg.ModelEndpoint != "https://api.openai.com/v1" {
		t.Fatalf("legacy main fields not synced: %q %q", cfg.Model, cfg.ModelEndpoint)
	}
}

func TestEnvOverridesFileRoleBinding(t *testing.T) {
	tmp := t.TempDir()
	configFile := filepath.Join(tmp, "vibe-coder.env")
	clearRoleEnv(t)
	t.Setenv("VIBE_CODER_CONFIG", configFile)
	t.Setenv("VIBE_CODER_MODEL", "env-model")
	t.Setenv("OLLAMA_HOST", "http://env-ollama:11434")
	writeConfigFile(t, configFile,
		`ROLE_MAIN_PROVIDER="ollama"`,
		`ROLE_MAIN_MODEL="file-model"`,
		`PROVIDER_OLLAMA_ENDPOINT="http://file-ollama:11434"`,
		`ROLE_SIDECAR_MODEL="file-sidecar"`,
	)

	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.RoleModelName(RoleMain); got != "env-model" {
		t.Fatalf("the env model should win, got %q", got)
	}
	if cfg.OllamaHost != "http://env-ollama:11434" {
		t.Fatalf("the env ollama host should win, got %q", cfg.OllamaHost)
	}
	if got := cfg.RoleModelName(RoleSidecar); got != "file-sidecar" {
		t.Fatalf("the file sidecar binding should survive, got %q", got)
	}
}

func TestProviderKindAndRoleParsing(t *testing.T) {
	if kind, ok := ParseProviderKind("OpenAI-Compatible"); !ok || kind != ProviderOpenAI {
		t.Fatalf("unexpected kind parse: %q %v", kind, ok)
	}
	if _, ok := ParseProviderKind("wat"); ok {
		t.Fatal("expected an unknown kind to fail")
	}
	if KindForEndpoint("http://host:11434") != ProviderOllama {
		t.Fatal("expected the ollama kind")
	}
	if KindForEndpoint("http://host:8080/v1") != ProviderOpenAI {
		t.Fatal("expected the openai kind")
	}

	for raw, want := range map[string]Role{"MAIN": RoleMain, " sidecar ": RoleSidecar, "Vision": RoleVision} {
		got, ok := ParseRole(raw)
		if !ok || got != want {
			t.Fatalf("ParseRole(%q) = %q, %v", raw, got, ok)
		}
	}
	if _, ok := ParseRole("nope"); ok {
		t.Fatal("expected an unknown role to fail")
	}

	for name, want := range map[string]bool{
		"ollama":         true,
		"my-provider_2":  true,
		"Bad Name":       false,
		"":               false,
		"tilde-ñ":        false,
		"trailing-slash": true,
	} {
		if got := ValidProviderName(name); got != want {
			t.Fatalf("ValidProviderName(%q) = %v", name, got)
		}
	}

	for role, want := range map[Role]string{
		RoleMain:      CapabilityCompletion,
		RoleSidecar:   CapabilityCompletion,
		RoleDecision:  CapabilityDecision,
		RoleEmbedding: CapabilityEmbedding,
		RoleVision:    CapabilityVision,
	} {
		if got := CapabilityRequirement(role); got != want {
			t.Fatalf("CapabilityRequirement(%q) = %q", role, got)
		}
	}
	if got := CapabilityRequirement(Role("unknown")); got != "" {
		t.Fatalf("expected no requirement for an unknown role, got %q", got)
	}
	if got := NormalizeProviderName("  MyProvider "); got != "myprovider" {
		t.Fatalf("unexpected normalized name: %q", got)
	}
}
