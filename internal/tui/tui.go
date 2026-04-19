package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

type PlainUI struct {
	in  *os.File
	out io.Writer

	reader *bufio.Reader

	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
	rawFD    int
	rawState *term.State
}

type Decision int

const (
	DecisionDeny Decision = iota
	DecisionAllowOnce
	DecisionAllowAll
	DecisionDenyAll
	DecisionYesMode
)

func NewPlain() *PlainUI {
	return &PlainUI{
		in:     os.Stdin,
		out:    os.Stdout,
		reader: bufio.NewReader(os.Stdin),
		stopCh: make(chan struct{}),
		rawFD:  int(os.Stdin.Fd()),
	}
}

func (u *PlainUI) StreamAssistant(text string) {
	_, _ = io.WriteString(u.out, text)
}

func (u *PlainUI) EndAssistant() {
	_, _ = io.WriteString(u.out, "\n")
}

func (u *PlainUI) ShowToolCall(name string, params map[string]any) {
	_, _ = fmt.Fprintf(u.out, "-> %s(%v)\n", name, params)
}

func (u *PlainUI) ShowToolResult(name, output string, isError bool) {
	prefix := "ok"
	if isError {
		prefix = "error"
	}
	_, _ = fmt.Fprintf(u.out, "<- %s [%s]: %s\n", name, prefix, output)
}

func (u *PlainUI) GetInput(prompt string) (string, error) {
	_, _ = io.WriteString(u.out, prompt)
	line, err := u.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = trimLine(line)
	if strings.TrimSpace(line) != ";;" {
		return line, nil
	}

	lines := make([]string, 0, 8)
	for {
		_, _ = io.WriteString(u.out, "... ")
		next, err := u.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		next = trimLine(next)
		if strings.TrimSpace(next) == ";;" {
			break
		}
		lines = append(lines, next)
	}
	return strings.Join(lines, "\n"), nil
}

func (u *PlainUI) AskPermission(tool string, params map[string]any) Decision {
	_, _ = fmt.Fprintf(u.out, "Allow %s with params %v? [y]es/[n]o/[a]ll/[d]eny-all/[s]kip-all-confirm: ", tool, params)
	line, err := u.reader.ReadString('\n')
	if err != nil {
		return DecisionDeny
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return DecisionAllowOnce
	case "a", "all":
		return DecisionAllowAll
	case "d", "deny-all":
		return DecisionDenyAll
	case "s", "skip-all-confirm":
		return DecisionYesMode
	default:
		return DecisionDeny
	}
}

func (u *PlainUI) StartESCMonitor(interrupt func()) error {
	if !term.IsTerminal(u.rawFD) {
		return nil
	}

	u.mu.Lock()
	if u.rawState != nil {
		u.mu.Unlock()
		return nil
	}
	state, err := term.MakeRaw(u.rawFD)
	if err != nil {
		u.mu.Unlock()
		return fmt.Errorf("enable raw mode: %w", err)
	}
	u.rawState = state
	u.mu.Unlock()

	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-u.stopCh:
				return
			default:
			}
			n, err := u.in.Read(buf)
			if err != nil || n == 0 {
				return
			}
			if buf[0] == 0x1B {
				interrupt()
				return
			}
		}
	}()
	return nil
}

func (u *PlainUI) StopESCMonitor() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.rawState != nil {
		_ = term.Restore(u.rawFD, u.rawState)
		u.rawState = nil
	}
}

func (u *PlainUI) Stop() {
	u.stopOnce.Do(func() {
		close(u.stopCh)
	})
	u.StopESCMonitor()
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
