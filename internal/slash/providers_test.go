package slash

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// newProviderTestCtx builds a slash context whose config carries an initialized
// provider registry and role bindings, mirroring a loaded configuration.
func newProviderTestCtx(t *testing.T, answers ...string) (*Ctx, *config.Config, *bytes.Buffer) {
	t.Helper()

	dir := t.TempDir()
	cfg := &config.Config{
		OllamaHost:      "http://localhost:11434",
		Model:           "qwen3.5:4b",
		ContextWindow:   32768,
		Cwd:             dir,
		SessionsDir:     filepath.Join(dir, "sessions"),
		ConfigDir:       dir,
		ConfigFile:      filepath.Join(dir, "vibe-coder.env"),
		ToolsByModel:    map[string]bool{"qwen3.5:4b": true, "llama3.2:3b": true},
		VisionByModel:   map[string]bool{"moondream": true},
		ThinkingByModel: map[string]bool{"qwen3.5:4b": true},
		Providers: map[string]config.Provider{
			config.DefaultProviderName: {
				Name:     config.DefaultProviderName,
				Kind:     config.ProviderOllama,
				Endpoint: "http://localhost:11434",
			},
		},
		RoleModels: map[config.Role]config.RoleModel{
			config.RoleMain: {Provider: config.DefaultProviderName, Model: "qwen3.5:4b"},
		},
	}

	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:      cfg,
		Out:      &out,
		Models:   &fakeModelLister{models: providerTestModels()},
		Prompter: &fakePrompter{answers: answers},
	}
	return ctx, cfg, &out
}

func addTestProvider(t *testing.T, cfg *config.Config, name, endpoint string, kind config.ProviderKind) {
	t.Helper()
	if err := cfg.AddProvider(config.Provider{Name: name, Endpoint: endpoint, Kind: kind}); err != nil {
		t.Fatalf("AddProvider(%q) error: %v", name, err)
	}
}

// initTestRegistry gives a hand-built config the default provider registry so
// /setmodel and /provider work against it without going through Load.
func initTestRegistry(cfg *config.Config) {
	if cfg.OllamaHost == "" {
		cfg.OllamaHost = "http://localhost:11434"
	}
	cfg.Providers = map[string]config.Provider{
		config.DefaultProviderName: {Name: config.DefaultProviderName, Kind: config.ProviderOllama, Endpoint: cfg.OllamaHost},
	}
	if cfg.RoleModels == nil {
		cfg.RoleModels = map[config.Role]config.RoleModel{}
	}
}

// providerTestModels mirrors real Ollama capability tags, including the
// completion capability every chat-capable model reports.
func providerTestModels() []ollama.Model {
	return []ollama.Model{
		{Name: "qwen3.5:4b", Capabilities: []string{"completion", "tools", "thinking"}, CapabilitiesKnown: true},
		{Name: "llama3.2:3b", Capabilities: []string{"completion", "tools"}, CapabilitiesKnown: true},
		{Name: "moondream", Capabilities: []string{"completion", "vision"}, CapabilitiesKnown: true},
	}
}

func TestProvidersCommandListsProvidersAndRoles(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t)

	addTestProvider(t, cfg, "groq", "https://api.groq.com/openai/v1", config.ProviderOpenAI)
	if err := cfg.SetProviderAPIKey("groq", "secret-key"); err != nil {
		t.Fatalf("SetProviderAPIKey: %v", err)
	}
	if err := cfg.SetRoleModel(config.RoleDecision, "groq", "llama-3.3-70b-versatile"); err != nil {
		t.Fatalf("SetRoleModel: %v", err)
	}

	if _, _, err := Dispatch(ctx, "/providers"); err != nil {
		t.Fatalf("Dispatch(/providers) error: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Providers (2)",
		"(ollama)",
		"(openai)",
		"key: set",
		"main  qwen3.5:4b",
		"decision  llama-3.3-70b-versatile",
		"Unassigned roles",
		"sidecar    Fallback to Main  (set it with /setmodel sidecar MODEL)",
		"embedding  text-only retrieval (no encoder)  (set it with /setmodel embedding MODEL)",
		"rabbit     Fallback to Main  (set it with /setmodel rabbit MODEL)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s", want, got)
		}
	}
}

func TestProvidersCommandMarksVisionModels(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t)

	if err := cfg.SetRoleModel(config.RoleSidecar, config.DefaultProviderName, "moondream"); err != nil {
		t.Fatalf("SetRoleModel: %v", err)
	}
	if _, _, err := Dispatch(ctx, "/providers"); err != nil {
		t.Fatalf("Dispatch(/providers) error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "sidecar  moondream  vision") {
		t.Fatalf("expected the sidecar line to carry the vision tag, got:\n%s", got)
	}
}

func TestProviderAddWithPromptedEndpoint(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t, "https://api.groq.com/openai/v1", "")

	if _, _, err := Dispatch(ctx, "/provider add groq"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	provider, ok := cfg.Provider("groq")
	if !ok {
		t.Fatal("provider groq was not added")
	}
	if provider.Kind != config.ProviderOpenAI {
		t.Errorf("kind = %q, want openai", provider.Kind)
	}
	if provider.Endpoint != "https://api.groq.com/openai/v1" {
		t.Errorf("endpoint = %q", provider.Endpoint)
	}

	prompter, _ := ctx.Prompter.(*fakePrompter)
	wantAsked := []string{"Endpoint for groq: ", "API key for groq (Enter to skip): "}
	if len(prompter.asked) != len(wantAsked) {
		t.Fatalf("asked = %q, want %q", prompter.asked, wantAsked)
	}
	for i, want := range wantAsked {
		if prompter.asked[i] != want {
			t.Errorf("asked[%d] = %q, want %q", i, prompter.asked[i], want)
		}
	}

	if !strings.Contains(out.String(), `Provider "groq" added`) {
		t.Errorf("output missing confirmation:\n%s", out.String())
	}
}

func TestProviderEndpointAutoCreatesProvider(t *testing.T) {
	ctx, cfg, _ := newProviderTestCtx(t)

	if _, _, err := Dispatch(ctx, "/provider custom endpoint http://localhost:1234/v1"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	provider, ok := cfg.Provider("custom")
	if !ok {
		t.Fatal("provider custom was not created")
	}
	if provider.Kind != config.ProviderOpenAI {
		t.Errorf("kind = %q, want openai", provider.Kind)
	}
	if provider.Endpoint != "http://localhost:1234/v1" {
		t.Errorf("endpoint = %q", provider.Endpoint)
	}
}

func TestProviderEndpointRejectsInvalidURL(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t)

	if _, _, err := Dispatch(ctx, "/provider custom endpoint localhost:11434"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	if !strings.Contains(out.String(), "Invalid endpoint") {
		t.Errorf("output missing invalid endpoint message:\n%s", out.String())
	}
	if _, ok := cfg.Provider("custom"); ok {
		t.Error("provider custom should not exist after an invalid endpoint")
	}
	if cfg.OllamaHost != "http://localhost:11434" {
		t.Errorf("OllamaHost changed to %q", cfg.OllamaHost)
	}
}

func TestProviderRemoveRefusedWhenBound(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t)

	addTestProvider(t, cfg, "groq", "https://api.groq.com/openai/v1", config.ProviderOpenAI)
	if err := cfg.SetRoleModel(config.RoleDecision, "groq", "llama-3.3-70b-versatile"); err != nil {
		t.Fatalf("SetRoleModel: %v", err)
	}

	if _, _, err := Dispatch(ctx, "/provider groq remove"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if !strings.Contains(out.String(), "is in use by the decision role") {
		t.Errorf("output missing refusal:\n%s", out.String())
	}
	if _, ok := cfg.Provider("groq"); !ok {
		t.Fatal("provider groq was removed while bound")
	}

	out.Reset()
	cfg.ClearRole(config.RoleDecision)
	if _, _, err := Dispatch(ctx, "/provider groq remove"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if _, ok := cfg.Provider("groq"); ok {
		t.Error("provider groq should be removed after clearing the binding")
	}
	if !strings.Contains(out.String(), `Provider "groq" removed`) {
		t.Errorf("output missing removal confirmation:\n%s", out.String())
	}
}

func TestSetModelByNumberValidatesCapability(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t)

	if _, _, err := Dispatch(ctx, "/setmodel main 2"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if got := cfg.RoleModelName(config.RoleMain); got != "llama3.2:3b" {
		t.Fatalf("main role = %q, want llama3.2:3b", got)
	}
	if !strings.Contains(out.String(), "completion capability verified") {
		t.Errorf("output missing verified note:\n%s", out.String())
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/setmodel decision 2"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if !strings.Contains(out.String(), `does not report the "decision" capability`) {
		t.Errorf("output missing capability refusal:\n%s", out.String())
	}
	if cfg.RoleInUse(config.RoleDecision) {
		t.Error("decision role should not be bound after a capability refusal")
	}
}

func TestSetModelByNameOnOpenAIProviderIsUnverified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "llama-3.3-70b-versatile"}},
		})
	}))
	defer srv.Close()

	ctx, cfg, out := newProviderTestCtx(t)
	addTestProvider(t, cfg, "groq", srv.URL, config.ProviderOpenAI)

	if _, _, err := Dispatch(ctx, "/setmodel groq main llama-3.3-70b-versatile"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	binding := cfg.RoleBinding(config.RoleMain)
	if binding.Provider != "groq" || binding.Model != "llama-3.3-70b-versatile" {
		t.Fatalf("main binding = %+v", binding)
	}
	if !cfg.IsModelOpenAI() {
		t.Error("IsModelOpenAI() = false, want true")
	}
	if !strings.Contains(out.String(), "capability unverified") {
		t.Errorf("output missing unverified note:\n%s", out.String())
	}
}

func TestSetModelClearsRole(t *testing.T) {
	ctx, cfg, _ := newProviderTestCtx(t)

	if err := cfg.SetRoleModel(config.RoleSidecar, config.DefaultProviderName, "qwen3.5:4b"); err != nil {
		t.Fatalf("SetRoleModel: %v", err)
	}

	if _, _, err := Dispatch(ctx, "/setmodel sidecar off"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	if cfg.SidecarModel != "" {
		t.Errorf("SidecarModel = %q, want empty", cfg.SidecarModel)
	}
	if cfg.RoleInUse(config.RoleSidecar) {
		t.Error("sidecar role should be cleared")
	}
}

func TestModelsCommandForNamedProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "llama-3.3-70b-versatile"}},
		})
	}))
	defer srv.Close()

	ctx, cfg, out := newProviderTestCtx(t)
	addTestProvider(t, cfg, "groq", srv.URL, config.ProviderOpenAI)

	if _, _, err := Dispatch(ctx, "/models groq"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Models on groq (1)") {
		t.Errorf("output missing provider header:\n%s", got)
	}
	if !strings.Contains(got, "llama-3.3-70b-versatile") {
		t.Errorf("output missing model name:\n%s", got)
	}
}

func TestSetModelPickerKeepsCurrent(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t, "")

	if _, _, err := Dispatch(ctx, "/setmodel main"); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	if got := cfg.RoleModelName(config.RoleMain); got != "qwen3.5:4b" {
		t.Errorf("main role = %q, want qwen3.5:4b", got)
	}
	if !strings.Contains(out.String(), "main unchanged.") {
		t.Errorf("output missing unchanged message:\n%s", out.String())
	}
}

// S10: the embedding role is stored and validated like the others, but no
// consumer reads it yet.
func TestSetModelEmbeddingRoleIsStoredAndValidated(t *testing.T) {
	ctx, cfg, out := newProviderTestCtx(t)
	ctx.Models = &fakeModelLister{models: append(providerTestModels(),
		ollama.Model{Name: "nomic-embed-text", Capabilities: []string{"embedding"}, CapabilitiesKnown: true},
	)}

	// A model without the embedding capability is refused, and nothing changes.
	if _, _, err := Dispatch(ctx, "/setmodel embedding 1"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := out.String(); !strings.Contains(got, `does not report the "embedding" capability`) {
		t.Fatalf("expected a capability refusal, got %q", got)
	}
	if cfg.RoleInUse(config.RoleEmbedding) {
		t.Fatal("expected the embedding role to stay unbound")
	}

	// A model that advertises embedding binds.
	out.Reset()
	if _, _, err := Dispatch(ctx, "/setmodel embedding 4"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := cfg.RoleModelName(config.RoleEmbedding); got != "nomic-embed-text" {
		t.Fatalf("expected the embedding role bound to nomic-embed-text, got %q", got)
	}
	if !strings.Contains(out.String(), "embedding role set to nomic-embed-text") {
		t.Fatalf("expected a confirmation, got %q", out.String())
	}
}
