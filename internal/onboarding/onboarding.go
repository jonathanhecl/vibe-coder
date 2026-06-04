package onboarding

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

const (
	defaultOllamaHost = "http://localhost:11434"
	defaultModel      = "qwen3.5:4b"
)

var ErrInterrupted = errors.New("first-run setup interrupted")

type wizard struct {
	reader *bufio.Reader
	out    io.Writer
	style  tui.Style
}

func RunFirstRun(ctx context.Context, cfg *config.Config, buildVersion string, in io.Reader, out io.Writer) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	w := &wizard{
		reader: bufio.NewReader(in),
		out:    out,
		style:  tui.NewStyle(out),
	}
	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	w.printIntro(buildVersion)
	host, client, models, err := w.selectHostAndModels(sigCtx, cfg.OllamaHost)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ErrInterrupted
		}
		return err
	}
	cfg.OllamaHost = host

	mainModel, err := w.chooseMainModel(sigCtx, client, models)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ErrInterrupted
		}
		return err
	}
	cfg.Model = mainModel

	updatedModels, err := client.Tags(sigCtx)
	if err != nil {
		fmt.Fprintf(w.out, "warning: could not refresh model list after selection: %v\n", err)
		updatedModels = models
	}
	sidecarModel, sidecarDisabled, err := w.chooseSidecarModel(sigCtx, client, updatedModels)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ErrInterrupted
		}
		return err
	}
	cfg.SidecarModel = sidecarModel
	cfg.SidecarDisabled = sidecarDisabled
	cfg.SidecarSkipSession = false

	if err := config.SaveModelSettings(cfg); err != nil {
		return fmt.Errorf("save first-run settings: %w", err)
	}
	w.printFinal(cfg)
	return nil
}

func (w *wizard) resolveModelCapabilities(ctx context.Context, client ollama.Client, models []ollama.Model) []ollama.Model {
	timeoutCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return ollama.ResolveModelCapabilities(timeoutCtx, client, models)
}
