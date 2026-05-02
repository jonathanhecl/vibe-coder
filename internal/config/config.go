package config

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
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

func autoDetectModel() string {
	ramGB := detectRAMGB()
	switch {
	case ramGB >= 48:
		return "qwen3.5:27b"
	case ramGB >= 24:
		return "qwen3.5:9b"
	case ramGB >= 12:
		return "qwen3.5:4b"
	default:
		return "qwen3.5:2b"
	}
}

func detectRAMGB() int {
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_RAM_GB")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			return parsed
		}
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		_ = bi
	}
	// Conservative fallback for portability; users can override with VIBE_CODER_RAM_GB.
	return 8
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

func resolveDirs(goos, homeDir, localAppData string) (string, string, error) {
	if goos == "windows" {
		if localAppData == "" {
			return "", "", errors.New("LOCALAPPDATA is empty")
		}
		base := filepath.Join(localAppData, "vibe-coder")
		return base, base, nil
	}
	if homeDir == "" {
		return "", "", errors.New("home directory is empty")
	}
	configDir := filepath.Join(homeDir, ".config", "vibe-coder")
	stateDir := filepath.Join(homeDir, ".local", "state", "vibe-coder")
	return configDir, stateDir, nil
}

func envFirstNonEmpty(keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func applyEnv(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_HOST")); v != "" {
		cfg.OllamaHost = v
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_MODEL", "VIBEGO_MODEL")); v != "" {
		cfg.Model = v
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_UI", "VIBEGO_UI")); v != "" {
		cfg.UI = v
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_SIDECAR_MODEL", "VIBEGO_SIDECAR_MODEL")); v != "" {
		cfg.SidecarModel = v
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_SIDECAR_DISABLED")); v != "" {
		if b, ok := parseBoolish(v); ok {
			cfg.SidecarDisabled = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_SIDECAR_ENABLED")); v != "" {
		if b, ok := parseBoolish(v); ok {
			cfg.SidecarDisabled = !b
		}
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_DEBUG", "VIBEGO_DEBUG")); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Debug = parsed
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_CHAT_TIMEOUT")); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
			cfg.ChatTimeout = parsed
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_NO_THINK")); v != "" {
		if b, ok := parseBoolish(v); ok && b {
			cfg.OllamaNoThink = true
		}
	}
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
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan config file: %w", err)
	}
	return nil
}

// parseBoolish parses common truthy/falsey strings for env/config keys.
func parseBoolish(s string) (value bool, ok bool) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

type cliOptions struct {
	prompt        optionalString
	interactive   optionalBool
	model         optionalString
	ui            optionalString
	sidecar       optionalString
	noSidecar     bool
	yesMode       optionalBool
	debug         optionalBool
	resume        optionalBool
	sessionID     optionalString
	listSessions  optionalBool
	ollamaHost    optionalString
	maxTokens     optionalInt
	temperature   optionalFloat
	contextWindow optionalInt
	rag           optionalBool
	ragMode       optionalString
	ragPath       optionalString
	ragTopK       optionalInt
	ragModel      optionalString
	ragIndex      optionalString
	help          optionalBool
	version       optionalBool
	noThink       bool
}

func parseCLI(args []string) (cliOptions, error) {
	var opts cliOptions

	fs := flag.NewFlagSet("vibe-coder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	fs.Var(&opts.prompt, "p", "one-shot prompt")
	fs.Var(&opts.interactive, "i", "interactive mode")
	fs.Var(&opts.interactive, "interactive", "interactive mode")
	fs.Var(&opts.model, "m", "model")
	fs.Var(&opts.model, "model", "model")
	fs.Var(&opts.ui, "ui", "ui mode (plain|rich)")
	fs.Var(&opts.sidecar, "sidecar", "sidecar model")
	fs.BoolVar(&opts.noSidecar, "no-sidecar", false, "disable sidecar for this session only")
	fs.Var(&opts.yesMode, "y", "yes mode")
	fs.Var(&opts.debug, "debug", "debug logs")
	fs.Var(&opts.resume, "resume", "resume session")
	fs.Var(&opts.sessionID, "session-id", "session id")
	fs.Var(&opts.listSessions, "list-sessions", "list sessions")
	fs.Var(&opts.ollamaHost, "ollama-host", "ollama host")
	fs.Var(&opts.maxTokens, "max-tokens", "max tokens")
	fs.Var(&opts.temperature, "temperature", "temperature")
	fs.Var(&opts.contextWindow, "context-window", "context window")
	fs.Var(&opts.rag, "rag", "rag mode")
	fs.Var(&opts.ragMode, "rag-mode", "rag mode type")
	fs.Var(&opts.ragPath, "rag-path", "rag path")
	fs.Var(&opts.ragTopK, "rag-topk", "rag top-k")
	fs.Var(&opts.ragModel, "rag-model", "rag model")
	fs.Var(&opts.ragIndex, "rag-index", "rag index path")
	fs.Var(&opts.help, "help", "show help")
	fs.Var(&opts.version, "version", "show version")
	fs.BoolVar(&opts.noThink, "no-think", false, "disable Ollama native thinking")

	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if len(fs.Args()) > 0 {
		return opts, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func applyCLI(cfg *Config, cli cliOptions) {
	if cli.prompt.set {
		cfg.Prompt = cli.prompt.value
	}
	if cli.interactive.set {
		cfg.Interactive = cli.interactive.value
	}
	if cli.model.set {
		cfg.Model = cli.model.value
	}
	if cli.ui.set {
		cfg.UI = cli.ui.value
	}
	if cli.sidecar.set {
		cfg.SidecarModel = cli.sidecar.value
	}
	if cli.noSidecar {
		cfg.SidecarSkipSession = true
	}
	if cli.yesMode.set {
		cfg.YesMode = cli.yesMode.value
	}
	if cli.debug.set {
		cfg.Debug = cli.debug.value
	}
	if cli.resume.set {
		cfg.Resume = cli.resume.value
	}
	if cli.sessionID.set {
		cfg.SessionID = cli.sessionID.value
	}
	if cli.listSessions.set {
		cfg.ListSessions = cli.listSessions.value
	}
	if cli.ollamaHost.set {
		cfg.OllamaHost = cli.ollamaHost.value
	}
	if cli.maxTokens.set {
		cfg.MaxTokens = cli.maxTokens.value
	}
	if cli.temperature.set {
		cfg.Temperature = cli.temperature.value
	}
	if cli.contextWindow.set {
		cfg.ContextWindow = cli.contextWindow.value
	}
	if cli.rag.set {
		cfg.RAG = cli.rag.value
	}
	if cli.ragMode.set {
		cfg.RAGMode = cli.ragMode.value
	}
	if cli.ragPath.set {
		cfg.RAGPath = cli.ragPath.value
	}
	if cli.ragTopK.set {
		cfg.RAGTopK = cli.ragTopK.value
	}
	if cli.ragModel.set {
		cfg.RAGModel = cli.ragModel.value
	}
	if cli.ragIndex.set {
		cfg.RAGIndex = cli.ragIndex.value
	}
	if cli.help.set {
		cfg.ShowHelp = cli.help.value
	}
	if cli.version.set {
		cfg.ShowVer = cli.version.value
	}
	if cli.noThink {
		cfg.OllamaNoThink = true
	}
}

type optionalString struct {
	value string
	set   bool
}

func (o *optionalString) Set(v string) error {
	o.value = v
	o.set = true
	return nil
}
func (o *optionalString) String() string { return o.value }

type optionalBool struct {
	value bool
	set   bool
}

func (o *optionalBool) Set(v string) error {
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return err
	}
	o.value = parsed
	o.set = true
	return nil
}
func (o *optionalBool) String() string   { return strconv.FormatBool(o.value) }
func (o *optionalBool) IsBoolFlag() bool { return true }

type optionalInt struct {
	value int
	set   bool
}

func (o *optionalInt) Set(v string) error {
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return err
	}
	o.value = parsed
	o.set = true
	return nil
}
func (o *optionalInt) String() string { return strconv.Itoa(o.value) }

type optionalFloat struct {
	value float64
	set   bool
}

func (o *optionalFloat) Set(v string) error {
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return err
	}
	o.value = parsed
	o.set = true
	return nil
}
func (o *optionalFloat) String() string {
	return strconv.FormatFloat(o.value, 'f', -1, 64)
}

func validateUIMode(mode string) error {
	v := strings.ToLower(strings.TrimSpace(mode))
	if v == "" {
		return errors.New("ui mode cannot be empty")
	}
	switch v {
	case "plain", "rich":
		return nil
	default:
		return fmt.Errorf("invalid ui mode %q: expected plain or rich", mode)
	}
}

// SaveModelSettings persists runtime model settings to the vibe-coder config file.
// It updates MODEL, SIDECAR_MODEL, OLLAMA_HOST, and SIDECAR_DISABLED while preserving other keys/comments.
func SaveModelSettings(cfg *Config) error {
	path := strings.TrimSpace(cfg.ConfigFile)
	if path == "" {
		path = filepath.Join(cfg.ConfigDir, "config.env")
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
			// Strip; re-added below when SidecarDisabled is true.
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
	for _, key := range []string{"MODEL", "SIDECAR_MODEL", "OLLAMA_HOST"} {
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
