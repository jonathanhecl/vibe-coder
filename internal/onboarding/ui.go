package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func (w *wizard) printIntro(buildVersion string) {
	header := "vibe-coder " + strings.TrimSpace(buildVersion)
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "\n%s %s\n", w.style.BoldBrightGreen(">>"), w.style.BoldGreen(header))
		fmt.Fprintf(w.out, "%s\n", w.style.DimGreen("Welcome. Let's set up your first run."))
	} else {
		fmt.Fprintf(w.out, "\n%s\n", header)
		fmt.Fprintln(w.out, "Welcome. Let's set up your first run.")
	}
	fmt.Fprintln(w.out, "This assistant works with Ollama or Ollama-compatible hosts.")
}

func (w *wizard) printFinal(cfg *config.Config) {
	if w.style.Enabled() {
		fmt.Fprintln(w.out, "")
		fmt.Fprintln(w.out, w.style.BoldBrightGreen("┌────────────────────────────────────────────┐"))
		fmt.Fprintln(w.out, w.style.BoldBrightGreen("│  vibe-coder setup complete                 │"))
		fmt.Fprintln(w.out, w.style.BoldBrightGreen("└────────────────────────────────────────────┘"))
	} else {
		fmt.Fprintln(w.out, "\n--- vibe-coder setup complete ---")
	}
	fmt.Fprintf(w.out, "Saved settings to %s\n", cfg.ConfigFile)
	fmt.Fprintln(w.out)
}

func (w *wizard) section(title string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "\n%s %s\n", w.style.BoldMagenta("◆"), w.style.BoldYellow(title))
		return
	}
	fmt.Fprintf(w.out, "\n%s:\n", title)
}

func (w *wizard) label(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s\n", w.style.BoldCyan("•"), w.style.BoldCyan(text))
		return
	}
	fmt.Fprintln(w.out, text+":")
}

func (w *wizard) option(key, text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "  %s %s\n", w.style.BoldBrightGreen("["+key+"]"), text)
		return
	}
	fmt.Fprintf(w.out, "  [%s] %s\n", key, text)
}

func (w *wizard) subtle(text string) {
	if w.style.Enabled() {
		fmt.Fprintln(w.out, w.style.Dim(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func (w *wizard) warn(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s\n", w.style.BoldRed("!"), w.style.Yellow(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func (w *wizard) good(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s\n", w.style.BoldGreen("✓"), w.style.BrightGreen(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func (w *wizard) selected(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s %s\n", w.style.DimGreen("onboarding"), w.style.DimGreen("›"), w.style.BrightGreen(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func (w *wizard) prompt(ctx context.Context, label string) (string, error) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s", w.style.DimGreen("onboarding"), w.style.BoldBrightGreen("› "+label))
	} else {
		fmt.Fprint(w.out, label)
	}
	type lineResult struct {
		line string
		err  error
	}
	lineCh := make(chan lineResult, 1)
	go func() {
		line, err := w.reader.ReadString('\n')
		lineCh <- lineResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ErrInterrupted
	case res := <-lineCh:
		if res.err != nil {
			return "", res.err
		}
		return strings.TrimRight(strings.TrimRight(res.line, "\n"), "\r"), nil
	}
}
