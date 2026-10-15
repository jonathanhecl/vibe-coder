package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type fakeLSPReporter struct{ resolved map[string]string }

func (f fakeLSPReporter) Resolved() map[string]string { return f.resolved }

func newLSPStatusCtx(t *testing.T, lspCfg config.LSPConfig, reporter LSPReporter) (*Ctx, *bytes.Buffer) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp, LSP: lspCfg}
	var out bytes.Buffer
	return &Ctx{Cfg: cfg, Session: session.New(cfg), Out: &out, LSP: reporter}, &out
}

// TestStatusReportsLanguageServers covers the three states a user can be in:
// switched off, on but with nothing to run here, and resolved with the command
// that will actually be spawned.
func TestStatusReportsLanguageServers(t *testing.T) {
	cases := []struct {
		name     string
		lspCfg   config.LSPConfig
		reporter LSPReporter
		want     string
	}{
		{
			name:   "switched off",
			lspCfg: config.LSPConfig{Enabled: config.LSPOff, TimeoutMS: 5000},
			want:   "off (LSP_ENABLED=off)",
		},
		{
			name:   "nothing resolved",
			lspCfg: config.LSPConfig{Enabled: config.LSPAuto, AfterEdit: true, TimeoutMS: 5000},
			want:   "none for this workspace",
		},
		{
			name:     "on with no server to run",
			lspCfg:   config.LSPConfig{Enabled: config.LSPOn, TimeoutMS: 5000},
			reporter: fakeLSPReporter{resolved: map[string]string{}},
			want:     "none for this workspace (LSP_ENABLED=on)",
		},
		{
			name:   "resolved for this workspace",
			lspCfg: config.LSPConfig{Enabled: config.LSPAuto, AfterEdit: true, TimeoutMS: 5000},
			reporter: fakeLSPReporter{resolved: map[string]string{
				"typescript": "typescript-language-server --stdio",
				"go":         "gopls",
			}},
			// Families are sorted so the line is the same on every run.
			want: "go: gopls, typescript: typescript-language-server --stdio (after edit, 5000ms)",
		},
		{
			name:     "resolved without the automatic check",
			lspCfg:   config.LSPConfig{Enabled: config.LSPAuto, TimeoutMS: 2500},
			reporter: fakeLSPReporter{resolved: map[string]string{"go": "gopls"}},
			want:     "go: gopls (manual, 2500ms)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, out := newLSPStatusCtx(t, tc.lspCfg, tc.reporter)
			handled, shouldExit, err := Dispatch(ctx, "/status")
			if err != nil || !handled || shouldExit {
				t.Fatalf("unexpected result: handled=%t exit=%t err=%v", handled, shouldExit, err)
			}
			want := "Language servers: " + tc.want
			if !strings.Contains(out.String(), want) {
				t.Fatalf("status is missing %q:\n%s", want, out.String())
			}
		})
	}
}

// TestStatusLanguageServersColorInTheBanner keeps the styled layout honest: a
// resolved server is the good outcome, everything else is muted, so the row
// cannot claim success by accident.
func TestStatusLanguageServersColorInTheBanner(t *testing.T) {
	resolved, resolvedOut := newLSPStatusCtx(t,
		config.LSPConfig{Enabled: config.LSPAuto, AfterEdit: true, TimeoutMS: 5000},
		fakeLSPReporter{resolved: map[string]string{"go": "gopls"}})
	writeStatus(resolved, tui.NewStyleForTest(true))
	if !strings.Contains(resolvedOut.String(), "\x1b[32mgo: gopls") {
		t.Fatalf("expected a green resolved row, got %q", resolvedOut.String())
	}

	off, offOut := newLSPStatusCtx(t, config.LSPConfig{Enabled: config.LSPOff, TimeoutMS: 5000}, nil)
	writeStatus(off, tui.NewStyleForTest(true))
	if !strings.Contains(offOut.String(), "\x1b[90moff (LSP_ENABLED=off)") {
		t.Fatalf("expected a muted off row, got %q", offOut.String())
	}
}
