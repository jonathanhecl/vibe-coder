package onboarding

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

const (
	// optionWidth caps long model names so the list never wraps in a normal
	// terminal. Full names are still written to the config file.
	optionWidth = 74
	// ruleWidth is the width of the header underline.
	ruleWidth = 52
)

func (w *wizard) printIntro(buildVersion string) {
	version := strings.TrimSpace(buildVersion)
	if w.style.Enabled() {
		fmt.Fprintln(w.out)
		fmt.Fprintf(w.out, "  %s %s\n", w.style.BoldBrightGreen("vibe"), w.style.DimGreen(version))
		fmt.Fprintf(w.out, "  %s\n", w.style.Gray(strings.Repeat("─", ruleWidth)))
		fmt.Fprintf(w.out, "  %s\n", w.style.Bold("Welcome — let's set up your first run."))
		fmt.Fprintf(w.out, "  %s\n", w.style.Dim("Works with Ollama or any Ollama-compatible host."))
		return
	}
	fmt.Fprintf(w.out, "\nvibe %s\n", version)
	fmt.Fprintln(w.out, "Welcome. Let's set up your first run.")
	fmt.Fprintln(w.out, "This assistant works with Ollama or Ollama-compatible hosts.")
}

// section renders a numbered step header. Pass total=0 to omit the counter.
func (w *wizard) section(step, total int, title string) {
	if w.style.Enabled() {
		counter := ""
		if total > 0 {
			counter = fmt.Sprintf("step %d/%d  ", step, total)
		}
		fmt.Fprintf(w.out, "\n%s %s%s\n", w.style.BoldMagenta("◆"), w.style.Gray(counter), w.style.BoldYellow(title))
		return
	}
	if total > 0 {
		fmt.Fprintf(w.out, "\nStep %d/%d: %s:\n", step, total, title)
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

// option renders a numbered menu entry. Optional tags (for example
// "recommended") are shown dimmed and are ignored in plain output.
func (w *wizard) option(key, text string, tags ...string) {
	text = truncateRunes(text, optionWidth)
	if w.style.Enabled() {
		line := fmt.Sprintf("  %s %s", w.style.BoldBrightGreen("["+key+"]"), text)
		if len(tags) > 0 {
			line += "  " + w.style.Dim(strings.Join(tags, " · "))
		}
		fmt.Fprintln(w.out, line)
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

// hint prints a muted, indented aside next to a prompt.
func (w *wizard) hint(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "  %s\n", w.style.Gray(text))
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
		fmt.Fprintf(w.out, "%s %s %s\n", w.style.DimGreen("vibe"), w.style.DimGreen("›"), w.style.BrightGreen(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

type kvRow struct {
	Key   string
	Value string
}

// summaryBox prints the closing summary. The styled variant draws a box sized
// to its content; plain output stays line-based for logs and pipes.
func (w *wizard) summaryBox(title string, rows []kvRow) {
	if !w.style.Enabled() {
		fmt.Fprintf(w.out, "\n--- %s ---\n", title)
		for _, r := range rows {
			fmt.Fprintf(w.out, "%s: %s\n", r.Key, r.Value)
		}
		return
	}

	keyWidth := 0
	width := utf8.RuneCountInString(title)
	for _, r := range rows {
		if l := utf8.RuneCountInString(r.Key); l > keyWidth {
			keyWidth = l
		}
	}
	for _, r := range rows {
		if l := keyWidth + 2 + utf8.RuneCountInString(r.Value); l > width {
			width = l
		}
	}
	if width < 34 {
		width = 34
	}

	border := func(s string) string { return w.style.Cyan(s) }
	hr := func(left, right string) string {
		return border(left + strings.Repeat("─", width+2) + right)
	}

	fmt.Fprintln(w.out)
	fmt.Fprintln(w.out, hr("╭", "╮"))
	titleContent := " " + title + strings.Repeat(" ", width-utf8.RuneCountInString(title)+1)
	fmt.Fprintln(w.out, border("│")+w.style.BoldBrightGreen(titleContent)+border("│"))
	fmt.Fprintln(w.out, hr("├", "┤"))
	for _, r := range rows {
		valPad := width - keyWidth - 2 - utf8.RuneCountInString(r.Value)
		content := " " + w.style.BoldCyan(r.Key) +
			strings.Repeat(" ", keyWidth-utf8.RuneCountInString(r.Key)) +
			"  " + w.style.BrightWhite(r.Value) + strings.Repeat(" ", valPad) + " "
		fmt.Fprintln(w.out, border("│")+content+border("│"))
	}
	fmt.Fprintln(w.out, hr("╰", "╯"))
}

// pullProgress redraws a single progress line for streamed downloads. It only
// runs on an interactive terminal; plain output keeps the status lines.
func (w *wizard) pullProgress(model string, ev ollama.PullEvent) {
	if !w.style.Enabled() || ev.Total <= 0 {
		return
	}
	pct := float64(ev.Completed) / float64(ev.Total)
	switch {
	case pct < 0:
		pct = 0
	case pct > 1:
		pct = 1
	}
	filled := int(pct * 20)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 20-filled)
	line := fmt.Sprintf("%s %s [%s] %3.0f%%", w.style.BoldCyan("↓"), w.style.BrightWhite(model), w.style.BoldBrightGreen(bar), pct*100)
	fmt.Fprintf(w.out, "%s%s", w.style.ClearPendingLine(), line)
}

func (w *wizard) prompt(ctx context.Context, label string) (string, error) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "  %s %s", w.style.BoldCyan("›"), w.style.BoldBrightGreen(label))
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

// truncateRunes shortens text to max runes, appending an ellipsis when cut.
func truncateRunes(text string, max int) string {
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max-1]) + "…"
}

func (w *wizard) printFinal(cfg *config.Config) {
	sidecar := "(disabled)"
	if cfg != nil && !cfg.SidecarDisabled && strings.TrimSpace(cfg.SidecarModel) != "" {
		sidecar = strings.TrimSpace(cfg.SidecarModel)
	}
	host := ""
	model := ""
	if cfg != nil {
		host = cfg.OllamaHost
		model = cfg.Model
	}

	w.summaryBox("vibe is ready", []kvRow{
		{Key: "Host", Value: host},
		{Key: "Model", Value: model},
		{Key: "Sidecar", Value: sidecar},
	})

	if cfg != nil && cfg.ConfigFile != "" {
		if w.style.Enabled() {
			fmt.Fprintf(w.out, "%s %s\n", w.style.DimGreen("settings saved to"), w.style.Gray(cfg.ConfigFile))
		} else {
			fmt.Fprintf(w.out, "Settings saved to %s\n", cfg.ConfigFile)
		}
	}
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s\n", w.style.DimGreen("start with"), w.style.BoldBrightGreen("vibe"))
	}
	fmt.Fprintln(w.out)
}
