package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type cliOptions struct {
	prompt            optionalString
	interactive       optionalBool
	model             optionalString
	ui                optionalString
	sidecar           optionalString
	noSidecar         bool
	decisionModel     optionalString
	decisionEndpoint  optionalString
	yesMode           optionalBool
	assistedYes       optionalBool
	permissionTimeout optionalDuration
	offline           optionalBool
	debug             optionalBool
	resume            optionalBool
	newSession        optionalBool
	temporal          optionalBool
	isolated          optionalBool
	sessionID         optionalString
	listSessions      optionalBool
	ollamaHost        optionalString
	endpoint          optionalString
	modelProvider     optionalString
	sidecarEndpoint   optionalString
	sidecarProvider   optionalString
	maxTokens         optionalInt
	temperature       optionalFloat
	contextWindow     optionalInt
	keepAliveMain     optionalInt
	keepAliveWorker   optionalInt
	rag               optionalBool
	ragMode           optionalString
	ragPath           optionalString
	ragTopK           optionalInt
	ragIndex          optionalString
	contextFiles      stringSlice
	skillsDirs        optionalString
	lsp               bool
	noLSP             bool
	lspServers        optionalString
	lspTimeout        optionalInt
	noLSPAfterEdit    bool
	help              optionalBool
	version           optionalBool
	thinkLevel        optionalString
	noThink           bool
	hideThink         bool
	showThink         bool
}

func parseCLI(args []string) (cliOptions, error) {
	var opts cliOptions

	fs := flag.NewFlagSet("vibe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	fs.Var(&opts.prompt, "p", "one-shot prompt")
	fs.Var(&opts.prompt, "prompt", "one-shot prompt")
	fs.Var(&opts.interactive, "i", "interactive mode")
	fs.Var(&opts.interactive, "interactive", "interactive mode")
	fs.Var(&opts.model, "m", "model")
	fs.Var(&opts.model, "model", "model")
	fs.Var(&opts.ui, "ui", "ui mode (plain|rich)")
	fs.Var(&opts.sidecar, "sidecar", "sidecar model")
	fs.BoolVar(&opts.noSidecar, "no-sidecar", false, "disable sidecar for this session only")
	fs.Var(&opts.decisionModel, "decision-model", "decision model name (Ollama model with the \"decision\" capability)")
	fs.Var(&opts.decisionEndpoint, "decision-endpoint", "decision model endpoint URL (Ollama host or custom System One server)")
	fs.Var(&opts.yesMode, "y", "yes mode")
	fs.Var(&opts.yesMode, "yes", "yes mode")
	fs.Var(&opts.assistedYes, "assisted-yes", "enable assisted execution mode with the decision model")
	fs.Var(&opts.assistedYes, "assisted", "enable assisted execution mode with the decision model (alias)")
	fs.Var(&opts.permissionTimeout, "permission-timeout", "how long a permission prompt waits before denying the action (e.g. 3m, off to wait forever)")
	fs.Var(&opts.offline, "offline", "enable offline mode (blocks network tools and external commands)")
	fs.Var(&opts.debug, "debug", "debug logs")
	fs.Var(&opts.newSession, "n", "start a new session (do not auto-resume)")
	fs.Var(&opts.newSession, "new", "start a new session (do not auto-resume)")
	fs.Var(&opts.temporal, "temporal", "start a temporal session (discards session history on exit)")
	fs.Var(&opts.temporal, "temp", "start a temporal session (alias)")
	fs.Var(&opts.temporal, "t", "start a temporal session (shorthand)")
	fs.Var(&opts.isolated, "isolated", "run an isolated session stored only in the current directory")
	fs.Var(&opts.isolated, "isolate", "run an isolated session (alias)")
	fs.Var(&opts.resume, "resume", "resume session")
	fs.Var(&opts.sessionID, "session-id", "session id")
	fs.Var(&opts.listSessions, "list-sessions", "list sessions")
	fs.Var(&opts.ollamaHost, "ollama-host", "ollama host")
	fs.Var(&opts.endpoint, "endpoint", "main model endpoint URL (Ollama or OpenAI-compatible /v1)")
	fs.Var(&opts.endpoint, "model-endpoint", "main model endpoint URL (alias)")
	fs.Var(&opts.modelProvider, "provider", "main model provider: ollama|openai (auto-detected by default)")
	fs.Var(&opts.modelProvider, "model-provider", "main model provider (alias)")
	fs.Var(&opts.sidecarEndpoint, "sidecar-endpoint", "sidecar model endpoint URL")
	fs.Var(&opts.sidecarProvider, "sidecar-provider", "sidecar model provider: ollama|openai")
	fs.Var(&opts.maxTokens, "max-tokens", "max tokens")
	fs.Var(&opts.temperature, "temperature", "temperature")
	fs.Var(&opts.contextWindow, "context-window", "context window")
	fs.Var(&opts.keepAliveMain, "keep-alive-main", "seconds to keep the main model loaded (-1 pins forever, 0 uses the provider default)")
	fs.Var(&opts.keepAliveWorker, "keep-alive-worker", "seconds to keep worker models loaded (-1 pins forever, 0 uses the provider default)")
	fs.Var(&opts.rag, "rag", "rag mode")
	fs.Var(&opts.ragMode, "rag-mode", "rag mode type")
	fs.Var(&opts.ragPath, "rag-path", "rag path")
	fs.Var(&opts.ragTopK, "rag-topk", "rag top-k")
	fs.Var(&opts.ragIndex, "rag-index", "rag index path")
	fs.Var(&opts.contextFiles, "context", "pin a .md/.txt guide file as persistent session instruction (repeatable)")
	fs.Var(&opts.skillsDirs, "skills-dirs", "extra directories scanned for skills (path-list separated)")
	fs.BoolVar(&opts.lsp, "lsp", false, "enable language-server diagnostics for this run")
	fs.BoolVar(&opts.noLSP, "no-lsp", false, "disable language-server diagnostics for this run")
	fs.Var(&opts.lspServers, "lsp-servers", "override a language server command (family:command, family:command)")
	fs.Var(&opts.lspTimeout, "lsp-timeout", "milliseconds to wait for the language server's report (250..60000)")
	fs.BoolVar(&opts.noLSPAfterEdit, "no-lsp-after-edit", false, "do not check diagnostics automatically after a written file")
	fs.Var(&opts.help, "help", "show help")
	fs.Var(&opts.version, "version", "show version")
	fs.Var(&opts.thinkLevel, "think", "thinking effort: off|low|medium|high|max")
	fs.Var(&opts.thinkLevel, "think-level", "thinking effort: off|low|medium|high|max")
	fs.BoolVar(&opts.noThink, "no-think", false, "disable Ollama native thinking")
	fs.BoolVar(&opts.hideThink, "hide-think", false, "hide Ollama thinking blocks in CLI output")
	fs.BoolVar(&opts.showThink, "show-think", false, "show Ollama thinking blocks in CLI output")

	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if opts.hideThink && opts.showThink {
		return opts, fmt.Errorf("cannot use both --hide-think and --show-think")
	}
	if opts.lsp && opts.noLSP {
		return opts, fmt.Errorf("cannot use both --lsp and --no-lsp")
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
		cfg.markRoleOverride(RoleMain)
	}
	if cli.ui.set {
		cfg.UI = cli.ui.value
	}
	if cli.sidecar.set {
		cfg.SidecarModel = cli.sidecar.value
		cfg.markRoleOverride(RoleSidecar)
	}
	if cli.noSidecar {
		cfg.SidecarSkipSession = true
	}
	if cli.decisionModel.set {
		cfg.DecisionModel = cli.decisionModel.value
		cfg.markRoleOverride(RoleDecision)
	}
	if cli.decisionEndpoint.set {
		cfg.DecisionEndpoint = cli.decisionEndpoint.value
		cfg.markRoleOverride(RoleDecision)
	}
	if cli.yesMode.set {
		cfg.YesMode = cli.yesMode.value
	}
	if cli.assistedYes.set {
		cfg.AssistedYes = cli.assistedYes.value
	}
	if cli.offline.set {
		cfg.Offline = cli.offline.value
	}
	if cli.debug.set {
		cfg.Debug = cli.debug.value
	}
	if cli.resume.set {
		cfg.Resume = cli.resume.value
	}
	if cli.newSession.set {
		cfg.NewSession = cli.newSession.value
	}
	if cli.temporal.set {
		cfg.Temporal = cli.temporal.value
	}
	if cfg.Temporal {
		cfg.NewSession = true
	}
	if cli.isolated.set {
		cfg.Isolated = cli.isolated.value
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
	if cli.endpoint.set {
		cfg.ModelEndpoint = cli.endpoint.value
		cfg.markRoleOverride(RoleMain)
	}
	if cli.modelProvider.set {
		cfg.ModelProvider = cli.modelProvider.value
		cfg.markRoleOverride(RoleMain)
	}
	if cli.sidecarEndpoint.set {
		cfg.SidecarEndpoint = cli.sidecarEndpoint.value
		cfg.markRoleOverride(RoleSidecar)
	}
	if cli.sidecarProvider.set {
		cfg.SidecarProvider = cli.sidecarProvider.value
		cfg.markRoleOverride(RoleSidecar)
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
	if cli.keepAliveMain.set {
		cfg.KeepAliveMain = cli.keepAliveMain.value
	}
	if cli.keepAliveWorker.set {
		cfg.KeepAliveWorker = cli.keepAliveWorker.value
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
	if cli.ragIndex.set {
		cfg.RAGIndex = cli.ragIndex.value
	}
	if len(cli.contextFiles.values) > 0 {
		cfg.ContextFiles = append(cfg.ContextFiles, cli.contextFiles.values...)
	}
	if cli.skillsDirs.set {
		cfg.SkillsDirs = splitPathList(cli.skillsDirs.value)
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
	if cli.thinkLevel.set {
		cfg.OllamaThinkLevel = cli.thinkLevel.value
	}
	if cli.hideThink {
		cfg.OllamaHideThink = true
	}
	if cli.showThink {
		cfg.OllamaHideThink = false
	}
	if cli.lsp {
		cfg.LSP.Enabled = LSPOn
	}
	if cli.noLSP {
		cfg.LSP.Enabled = LSPOff
	}
	if cli.lspServers.set {
		cfg.LSP.Servers = parseLSPServers(cli.lspServers.value)
	}
	if cli.lspTimeout.set {
		cfg.LSP.TimeoutMS = cli.lspTimeout.value
	}
	if cli.noLSPAfterEdit {
		cfg.LSP.AfterEdit = false
	}
	if cli.permissionTimeout.set {
		cfg.PermissionTimeout = cli.permissionTimeout.value
	}
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

// optionalDuration accepts a Go duration (or off/never) so a flag can carry the
// same syntax as the config file and the environment.
type optionalDuration struct {
	value time.Duration
	set   bool
}

func (o *optionalDuration) Set(v string) error {
	parsed, ok := ParsePermissionTimeout(v)
	if !ok {
		return fmt.Errorf("invalid duration %q: use a Go duration such as 90s or 3m, or off", v)
	}
	o.value = parsed
	o.set = true
	return nil
}

func (o *optionalDuration) String() string {
	if o.value == 0 {
		return ""
	}
	return o.value.String()
}

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

// stringSlice collects repeatable string flags such as --context.
// Each occurrence appends one value; commas are preserved so paths
// containing commas keep working.
type stringSlice struct {
	values []string
	set    bool
}

func (s *stringSlice) Set(v string) error {
	s.values = append(s.values, v)
	s.set = true
	return nil
}

func (s *stringSlice) String() string { return strings.Join(s.values, ", ") }
