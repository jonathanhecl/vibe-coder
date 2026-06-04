package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

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
	fs.Var(&opts.prompt, "prompt", "one-shot prompt")
	fs.Var(&opts.interactive, "i", "interactive mode")
	fs.Var(&opts.interactive, "interactive", "interactive mode")
	fs.Var(&opts.model, "m", "model")
	fs.Var(&opts.model, "model", "model")
	fs.Var(&opts.ui, "ui", "ui mode (plain|rich)")
	fs.Var(&opts.sidecar, "sidecar", "sidecar model")
	fs.BoolVar(&opts.noSidecar, "no-sidecar", false, "disable sidecar for this session only")
	fs.Var(&opts.yesMode, "y", "yes mode")
	fs.Var(&opts.yesMode, "yes", "yes mode")
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
