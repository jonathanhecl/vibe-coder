package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
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

func (u *PlainUI) GetInput(prompt string) (string, error) {
	_, _ = io.WriteString(u.out, prompt)
	line, err := u.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return trimLine(line), nil
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
