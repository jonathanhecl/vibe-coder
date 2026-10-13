package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

const (
	defaultOllamaHost    = "http://localhost:11434"
	defaultMaxTokens     = 8192
	defaultTemperature   = 0.7
	defaultContextWindow = 32768
	maxConfigFileSize    = 64 * 1024
)

type Config struct {
	OllamaHost   string
	Model        string
	UI           string
	SidecarModel string
	// ModelEndpoint overrides the host/endpoint for the main model (defaults to OllamaHost).
	ModelEndpoint string
	// ModelApiKey holds an optional API key for the main model endpoint.
	ModelApiKey string
	// ModelProvider explicitly specifies "ollama" or "openai" for the main model (auto-detected if unset).
	ModelProvider string
	// SidecarEndpoint overrides the host/endpoint for the sidecar model.
	SidecarEndpoint string
	// SidecarApiKey holds an optional API key for the sidecar endpoint.
	SidecarApiKey string
	// SidecarProvider explicitly specifies "ollama" or "openai" for the sidecar model (auto-detected if unset).
	SidecarProvider string
	// DecisionModel holds the optional decision model name (Ollama model with the "decision" capability).
	DecisionModel string
	// DecisionEndpoint holds the optional decision model endpoint URL.
	// When empty and DecisionModel is set, the main Ollama host is used.
	DecisionEndpoint string
	// DecisionAPIKey holds an optional bearer token for the decision endpoint.
	DecisionAPIKey string
	// LegacyJevstyle reports that removed JEVSTYLE_* config keys or env vars
	// were found during load; the caller surfaces a one-time warning.
	LegacyJevstyle   bool
	ConfigFileExists bool
	// SidecarDisabled, when true, turns off the sidecar until changed in config (SIDECAR_DISABLED / SIDECAR_ENABLED).
	SidecarDisabled bool
	// SidecarSkipSession skips the sidecar for this process only (--no-sidecar, /sidecar off); not persisted.
	SidecarSkipSession bool
	MaxTokens          int
	Temperature        float64
	ContextWindow      int
	Prompt             string
	Interactive        bool
	YesMode            bool
	AssistedYes        bool
	Debug              bool
	Resume             bool
	NewSession         bool
	// Temporal runs an ephemeral session without persisting session history
	// or updating project session indexes on exit.
	Temporal bool
	// Isolated runs a folder-local session stored exclusively in <cwd>/.vibe-isolated.jsonl
	// rather than in the global state sessions directory.
	Isolated bool
	// Offline blocks external network tools and shell commands accessing the internet.
	Offline      bool
	SessionID    string
	ListSessions bool
	Cwd          string
	// ContextFiles holds user-provided guide files pinned as persistent
	// session instructions (repeatable --context flag). Contents are
	// injected into the system prompt and never compacted.
	ContextFiles []string
	// VisionAvailable reports whether the active model advertises vision
	// capability. VisionKnown is false when detection failed (e.g. Ollama
	// unreachable at startup); the agent then stays honest about trying.
	VisionAvailable bool
	VisionKnown     bool
	// VisionByModel caches vision support per model name so /model switches
	// resolve without extra network calls.
	VisionByModel map[string]bool
	// SidecarVisionAvailable/Known mirror the main vision flags for the
	// sidecar model. When the main model cannot see but the sidecar can,
	// attached images arrive as sidecar-generated descriptions.
	SidecarVisionAvailable bool
	SidecarVisionKnown     bool

	RAG         bool
	RAGModel    string
	RAGPath     string
	RAGTopK     int
	RAGIndex    string
	RAGMode     string
	ShowHelp    bool
	ShowVer     bool
	ConfigDir   string
	ConfigFile  string
	StateDir    string
	SessionsDir string
	PermFile    string
	HistoryFile string
	// ChatTimeout caps one streaming chat call to Ollama (0 = use default, see EffectiveChatTimeout).
	ChatTimeout time.Duration
	// OllamaNoThink disables native thinking in /api/chat when false is sent (faster replies; quality trade-off).
	OllamaNoThink bool
	// OllamaThinkLevel selects the Ollama think value: "" (unset, default
	// thinking), "off", "on", "low", "medium", "high" or "max". An explicit
	// level wins over OllamaNoThink. Persisted via THINK with --save.
	OllamaThinkLevel string
	// ThinkingSupported/ThinkingKnown describe the active model's thinking
	// capability as advertised by Ollama Tags. Unknown until detection runs.
	ThinkingSupported bool
	ThinkingKnown     bool
	// ThinkingByModel caches thinking support per model name so /model and
	// /think resolve without extra network calls.
	ThinkingByModel map[string]bool
	// ToolsSupported/ToolsKnown describe the active model's native function-
	// calling capability as advertised by Ollama Tags. Unknown until
	// detection runs; the agent sends native tools optimistically unless
	// the model is known-unsupported (the client also falls back per
	// session on a 400 tool error).
	ToolsSupported bool
	ToolsKnown     bool
	// ToolsByModel caches native tool support per model name so /model
	// switches resolve without extra network calls.
	ToolsByModel map[string]bool
	// OllamaHideThink hides native and in-band thinking blocks in TUI (shows response without thinking section).
	OllamaHideThink bool
	// Harness holds the agent-harness tunables (context budget, tool-result
	// retention, metrics). Precedence: defaults < config file < env.
	Harness HarnessConfig
}

// HarnessConfig centralizes the agent-harness tunables so they can be set
// from the config file or the environment instead of being hardcoded.
type HarnessConfig struct {
	// KeepToolResults bounds how many recent tool results stay unmasked in
	// the transcript window (default 6; values <1 fall back to default).
	KeepToolResults int
	// ToolOutputMaxTokens caps a single tool result's token size
	// (default 0 = auto; negative values normalize to 0).
	ToolOutputMaxTokens int
	// CompactAtPercent is the context-window fill percentage that triggers
	// compaction (default 85, clamped to 50..95).
	CompactAtPercent int
	// MetricsDisabled turns off JSONL metrics recording (the in-memory
	// summary still updates).
	MetricsDisabled bool
}

const (
	defaultKeepToolResults  = 6
	defaultCompactAtPercent = 85
	minCompactAtPercent     = 50
	maxCompactAtPercent     = 95
)

// normalize clamps the harness tunables to their documented ranges.
func (h *HarnessConfig) normalize() {
	if h.KeepToolResults < 1 {
		h.KeepToolResults = defaultKeepToolResults
	}
	if h.ToolOutputMaxTokens < 0 {
		h.ToolOutputMaxTokens = 0
	}
	if h.CompactAtPercent < minCompactAtPercent {
		h.CompactAtPercent = minCompactAtPercent
	}
	if h.CompactAtPercent > maxCompactAtPercent {
		h.CompactAtPercent = maxCompactAtPercent
	}
}

func Load(args []string) (*Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve cwd: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	configDir, stateDir, err := resolveDirs(runtime.GOOS, homeDir, os.Getenv("LOCALAPPDATA"))
	if err != nil {
		return nil, err
	}

	cfg := defaultConfig(cwd, configDir, stateDir)

	configPath := envFirstNonEmpty("VIBE_CODER_CONFIG", "CONFIG")
	if configPath == "" {
		configPath = filepath.Join(cfg.ConfigDir, "vibe-coder.env")
	}
	cfg.ConfigFile = configPath
	exists, err := configFileExists(configPath)
	if err != nil {
		return nil, err
	}
	cfg.ConfigFileExists = exists
	if strings.TrimSpace(cfg.PermFile) == "" {
		cfg.PermFile = cfg.ConfigFile
	}

	if err := applyConfigFile(cfg, configPath); err != nil {
		return nil, err
	}

	applyEnv(cfg)
	cfg.Harness.normalize()

	cli, err := parseCLI(args)
	if err != nil {
		return nil, err
	}
	applyCLI(cfg, cli)

	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = autoDetectModel()
	}
	if err := validateUIMode(cfg.UI); err != nil {
		return nil, err
	}
	if norm, ok := ollama.NormalizeThinkLevel(cfg.OllamaThinkLevel); !ok {
		return nil, fmt.Errorf("invalid think level %q: expected off|low|medium|high|max", cfg.OllamaThinkLevel)
	} else {
		cfg.OllamaThinkLevel = norm
	}

	if _, err := url.ParseRequestURI(cfg.OllamaHost); err != nil {
		return nil, fmt.Errorf("invalid ollama host %q: %w", cfg.OllamaHost, err)
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	return cfg, nil
}

func Usage(binName string) string {
	return fmt.Sprintf(`Usage:
  %[1]s [flags]

Flags:
  --version                 Print version and exit
  --help                    Show this help and exit
  --ui string               UI mode (plain|rich)
  -p, --prompt string       One-shot prompt
  -i, --interactive         Interactive mode (with -p, keep chatting)
  -m, --model string        Model name
  --sidecar string          Sidecar model name
  --no-sidecar              Disable sidecar for this session only
  --decision-model string   Decision model name (Ollama model with the "decision" capability)
  --decision-endpoint string   Decision model endpoint URL (Ollama host or custom System One server)
  -y, --yes                 Enable yes mode
  --assisted-yes            Enable assisted execution mode with the decision model (auto-approve safe commands)
  --offline                 Enable offline mode (blocks network tools and external commands)
  --debug                   Enable debug logs
  -n, --new                 Start a new session (do not auto-resume previous session)
  --resume                  Resume last session for this project
  --session-id string       Resume specific session id
  --list-sessions           List known sessions
  --endpoint string         Main model endpoint URL (Ollama or OpenAI-compatible /v1)
  --provider string         Main model provider: ollama|openai (auto-detected by default)
  --sidecar-endpoint string Sidecar model endpoint URL
  --sidecar-provider string Sidecar model provider: ollama|openai
  --ollama-host string      Ollama base URL
  --max-tokens int          Max generated tokens
  --temperature float       Sampling temperature
  --context-window int      Model context window
  --context string          Pin a .md/.txt guide file as persistent session instruction (repeatable)
  --rag                     Enable RAG mode
  --rag-mode string         RAG mode
  --rag-path string         RAG path
  --rag-topk int            RAG top-k chunks
  --rag-model string        RAG embedding model
  --rag-index string        Build/index RAG path and exit
  --no-think                Disable Ollama native thinking (faster replies)
  --think <level>           Thinking effort: off|low|medium|high|max (default: model default)
  --hide-think              Hide Ollama thinking blocks in CLI output
  --show-think              Show Ollama thinking blocks in CLI output (overrides config/env)

Special directive:
  --save                    Persist model, sidecar, decision, host, hide-think, think to vibe-coder.env; with --no-sidecar also SIDECAR_DISABLED=true
`, binName)
}

func defaultConfig(cwd, configDir, stateDir string) *Config {
	return &Config{
		OllamaHost:    defaultOllamaHost,
		MaxTokens:     defaultMaxTokens,
		Temperature:   defaultTemperature,
		ContextWindow: defaultContextWindow,
		UI:            "plain",
		Cwd:           cwd,
		ConfigDir:     configDir,
		StateDir:      stateDir,
		SessionsDir:   filepath.Join(stateDir, "sessions"),
		// PermFile is set in Load() to ConfigFile so permissions live in config.env.
		PermFile:    "",
		HistoryFile: filepath.Join(stateDir, "history.txt"),
		Harness: HarnessConfig{
			KeepToolResults:  defaultKeepToolResults,
			CompactAtPercent: defaultCompactAtPercent,
		},
	}
}

func (c *Config) rawModelEndpoint() string {
	if c != nil && strings.TrimSpace(c.ModelEndpoint) != "" {
		return strings.TrimSpace(c.ModelEndpoint)
	}
	if c != nil && strings.TrimSpace(c.OllamaHost) != "" {
		return strings.TrimSpace(c.OllamaHost)
	}
	return defaultOllamaHost
}

func (c *Config) rawSidecarEndpoint() string {
	if c != nil && strings.TrimSpace(c.SidecarEndpoint) != "" {
		return strings.TrimSpace(c.SidecarEndpoint)
	}
	return c.rawModelEndpoint()
}

// EffectiveModelEndpoint returns the resolved endpoint URL for the main model.
func (c *Config) EffectiveModelEndpoint() string {
	raw := c.rawModelEndpoint()
	if c != nil && !c.IsModelOpenAI() {
		return ollama.NormalizeURL(raw)
	}
	return raw
}

// EffectiveSidecarEndpoint returns the resolved endpoint URL for the sidecar model.
func (c *Config) EffectiveSidecarEndpoint() string {
	raw := c.rawSidecarEndpoint()
	if c != nil && !c.IsSidecarOpenAI() {
		return ollama.NormalizeURL(raw)
	}
	return raw
}

// IsModelOpenAI reports whether the main model endpoint uses the OpenAI-compatible protocol.
func (c *Config) IsModelOpenAI() bool {
	if c == nil {
		return false
	}
	p := strings.ToLower(strings.TrimSpace(c.ModelProvider))
	if p == "openai" || p == "openai-compatible" {
		return true
	}
	if p == "ollama" {
		return false
	}
	return IsOpenAIEndpoint(c.rawModelEndpoint())
}

// IsSidecarOpenAI reports whether the sidecar model endpoint uses the OpenAI-compatible protocol.
func (c *Config) IsSidecarOpenAI() bool {
	if c == nil {
		return false
	}
	p := strings.ToLower(strings.TrimSpace(c.SidecarProvider))
	if p == "openai" || p == "openai-compatible" {
		return true
	}
	if p == "ollama" {
		return false
	}
	return IsOpenAIEndpoint(c.rawSidecarEndpoint())
}

// IsOpenAIEndpoint reports whether an endpoint URL matches OpenAI-compatible (/v1) conventions.
func IsOpenAIEndpoint(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	trimmed := strings.TrimRight(strings.ToLower(raw), "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return true
	}
	u, err := url.Parse(raw)
	if err == nil {
		p := strings.TrimRight(strings.ToLower(u.Path), "/")
		if strings.HasSuffix(p, "/v1") {
			return true
		}
	}
	return false
}
