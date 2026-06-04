package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultOllamaHost    = "http://localhost:11434"
	defaultMaxTokens     = 8192
	defaultTemperature   = 0.7
	defaultContextWindow = 32768
	maxConfigFileSize    = 64 * 1024
)

type Config struct {
	OllamaHost       string
	Model            string
	UI               string
	SidecarModel     string
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
	Debug              bool
	Resume             bool
	SessionID          string
	ListSessions       bool
	Cwd                string

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
		configPath = filepath.Join(cfg.ConfigDir, "config.env")
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
  -p string                 One-shot prompt
  -i, --interactive         Interactive mode (with -p, keep chatting)
  -m, --model string        Model name
  --sidecar string          Sidecar model name
  --no-sidecar              Disable sidecar for this session only
  -y                        Enable yes mode
  --debug                   Enable debug logs
  --resume                  Resume last session for this project
  --session-id string       Resume specific session id
  --list-sessions           List known sessions
  --ollama-host string      Ollama base URL
  --max-tokens int          Max generated tokens
  --temperature float       Sampling temperature
  --context-window int      Model context window
  --rag                     Enable RAG mode
  --rag-mode string         RAG mode
  --rag-path string         RAG path
  --rag-topk int            RAG top-k chunks
  --rag-model string        RAG embedding model
  --rag-index string        Build/index RAG path and exit
  --no-think                Disable Ollama native thinking (faster replies)

Special directive:
  /save                     Persist model, sidecar, host to config.env; with --no-sidecar also SIDECAR_DISABLED=true
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
	}
}
