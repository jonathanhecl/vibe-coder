package tui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
)

var testANSISeqRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripTestANSI(s string) string {
	return testANSISeqRE.ReplaceAllString(s, "")
}

func TestWrapANSILinePreservesStylesAndRespectsWidth(t *testing.T) {
	t.Parallel()

	// 1. Plain word wrap.
	plain := wrapANSILine("alpha beta gamma delta", 10)
	if len(plain) < 2 {
		t.Fatalf("expected wrapped lines, got %v", plain)
	}
	for i, ln := range plain {
		if displayWidth(ln) > 10 {
			t.Fatalf("line %d exceeds width 10: %d (%q)", i, displayWidth(ln), ln)
		}
		if strings.TrimRight(ln, " ") != ln {
			t.Fatalf("line %d has trailing spaces: %q", i, ln)
		}
	}

	// 2. Hard wrap of long unbreakable JSON/path.
	jsonLine := `{"path":"/very/long/unbreakable/directory/segment/file.txt"}`
	wrappedJSON := wrapANSILine(jsonLine, 18)
	if len(wrappedJSON) < 3 {
		t.Fatalf("expected hard-wrapped JSON lines, got %v", wrappedJSON)
	}
	for i, ln := range wrappedJSON {
		if displayWidth(ln) > 18 {
			t.Fatalf("JSON line %d exceeds width 18: %d (%q)", i, displayWidth(ln), ln)
		}
	}
	if strings.Join(wrappedJSON, "") != jsonLine {
		t.Fatalf("hard wrap lost characters: got %q want %q", strings.Join(wrappedJSON, ""), jsonLine)
	}

	// 3. ANSI color reset and re-open across wrapped lines.
	styled := "\x1b[31mhello world from ansi\x1b[0m"
	wrappedStyled := wrapANSILine(styled, 10)
	if len(wrappedStyled) < 2 {
		t.Fatalf("expected wrapped ANSI lines, got %v", wrappedStyled)
	}
	for i, ln := range wrappedStyled {
		if displayWidth(ln) > 10 {
			t.Fatalf("ANSI line %d exceeds width 10: %d (%q)", i, displayWidth(ln), ln)
		}
		if !strings.HasPrefix(ln, "\x1b[31m") {
			t.Fatalf("ANSI line %d missing reopened color prefix: %q", i, ln)
		}
		if !strings.HasSuffix(ln, "\x1b[0m") {
			t.Fatalf("ANSI line %d missing reset suffix: %q", i, ln)
		}
	}

	// 4. Wide runes, emojis with variation selector, Spanish accents, combining mark.
	unicodeLine := "🛡️ ✓ áéíóúñ e\u0301 " + strings.Repeat("語", 10)
	wrappedUni := wrapANSILine(unicodeLine, 12)
	for i, ln := range wrappedUni {
		if displayWidth(ln) > 12 {
			t.Fatalf("unicode line %d exceeds width 12: %d (%q)", i, displayWidth(ln), ln)
		}
	}

	// 5. Tabs and carriage returns normalized.
	tabbed := wrapANSILine("a\tb\rc", 20)
	if len(tabbed) != 1 || tabbed[0] != "a  bc" {
		t.Fatalf("unexpected tab/cr normalization: %v", tabbed)
	}
}

func TestHybridToolResultErrorNoBlankLinesAtNarrowWidth(t *testing.T) {
	t.Parallel()

	errOutput := "file_path is required\nattempted args (249 bytes): {\"append\":false,\"contents\":\"# Logs de prueba\\n# Aquí irán los logs del proceso de fine-tuning\\n\\n[INFO] Datos cargados exitosamente.\\n[INFO] Modelo listo para entrenamiento.\\n[INFO] Entrenamiento completado.\\n\"}"
	params := map[string]any{
		"append":   false,
		"contents": "# Logs de prueba\n# Aquí irán...",
	}

	for _, cols := range []int{60, 80, 100} {
		var out bytes.Buffer
		h := newStyledHybridForTest(&out)
		h.termColsOverride = cols
		h.ShowToolResult("Write", errOutput, true, params)

		raw := strings.TrimRight(out.String(), "\n")
		lines := strings.Split(raw, "\n")
		if len(lines) < 3 {
			t.Fatalf("cols=%d: expected wrapped multiline card, got %d lines:\n%s", cols, len(lines), raw)
		}
		var plainLines []string
		for i, ln := range lines {
			plain := stripTestANSI(ln)
			if strings.TrimSpace(plain) == "" {
				t.Fatalf("cols=%d: unexpected blank line %d in:\n%s", cols, i, raw)
			}
			if strings.TrimRight(plain, " ") != plain {
				t.Fatalf("cols=%d: line %d has trailing spaces: %q", cols, i, plain)
			}
			if !strings.HasPrefix(plain, " ┃ ") {
				t.Fatalf("cols=%d: line %d missing left rail prefix: %q", cols, i, plain)
			}
			if w := displayWidth(ln); w >= cols {
				t.Fatalf("cols=%d: line %d width %d >= %d: %q", cols, i, w, cols, plain)
			}
			plainLines = append(plainLines, strings.TrimPrefix(plain, " ┃ "))
		}
		joinedSpace := strings.Join(plainLines, " ")
		joinedCompact := strings.Join(plainLines, "")
		for _, want := range []string{"file_path is required", "attempted args (249 bytes):", "Entrenamiento completado"} {
			if !strings.Contains(joinedSpace, want) && !strings.Contains(joinedCompact, want) {
				t.Fatalf("cols=%d: missing %q in rendered card:\n%s", cols, want, raw)
			}
		}
	}
}

func TestHybridToolResultShellReadEditNoBlankLines(t *testing.T) {
	t.Parallel()

	longCmd := strings.Repeat("cd /some/long/path && ", 6) + "go test ./..."
	longTail := "line1\tcol2\r\nline2 " + strings.Repeat("ok_", 45) + "\r\nline3\nline4 done"
	hugeCmd := strings.Repeat("x", bashCommandRunes+40)
	longDiff := "@@ -1,2 +1,2 @@\n-" + strings.Repeat("oldCodeSegment()", 9) + "\n+" + strings.Repeat("newCodeSegment()", 9) + "\n"

	cases := []struct {
		name   string
		tool   string
		output string
		isErr  bool
		params map[string]any
	}{
		{
			name:   "BashSuccess",
			tool:   "Bash",
			output: longTail,
			isErr:  false,
			params: map[string]any{"command": longCmd},
		},
		{
			name:   "BashError",
			tool:   "Bash",
			output: "fatal: " + strings.Repeat("error_detail_", 12) + "\nsecond stderr line",
			isErr:  true,
			params: map[string]any{"command": hugeCmd},
		},
		{
			name:   "ReadLong",
			tool:   "Read",
			output: strings.Repeat("segment/", 20) + "result.txt",
			isErr:  false,
			params: map[string]any{"file_path": "/workspace/very/long/nested/directory/source_file.go"},
		},
		{
			name:   "ReadError",
			tool:   "Read",
			output: "open /workspace/very/long/nested/directory/missing_file_name_for_error_wrap.go: no such file or directory",
			isErr:  true,
			params: map[string]any{"file_path": "/workspace/very/long/nested/directory/missing_file_name_for_error_wrap.go"},
		},
		{
			name:   "EditDiff",
			tool:   "Edit",
			output: "Write successful.",
			isErr:  false,
			params: map[string]any{
				"file_path":  "internal/tui/hybrid_render.go",
				"old_string": "a",
				"new_string": "b",
				"_diff":      longDiff,
			},
		},
	}

	for _, tc := range cases {
		for _, cols := range []int{60, 80} {
			var out bytes.Buffer
			h := newStyledHybridForTest(&out)
			h.termColsOverride = cols
			h.ShowToolResult(tc.tool, tc.output, tc.isErr, tc.params)

			raw := strings.TrimRight(out.String(), "\n")
			lines := strings.Split(raw, "\n")
			if len(lines) == 0 {
				t.Fatalf("%s cols=%d: empty output", tc.name, cols)
			}
			for i, ln := range lines {
				plain := stripTestANSI(ln)
				if strings.TrimSpace(plain) == "" {
					t.Fatalf("%s cols=%d: phantom blank line %d in:\n%s", tc.name, cols, i, raw)
				}
				if strings.TrimRight(plain, " ") != plain {
					t.Fatalf("%s cols=%d: line %d has trailing spaces: %q", tc.name, cols, i, plain)
				}
				if !strings.HasPrefix(plain, " ┃ ") {
					t.Fatalf("%s cols=%d: line %d missing left rail prefix: %q", tc.name, cols, i, plain)
				}
				if w := displayWidth(ln); w >= cols {
					t.Fatalf("%s cols=%d: line %d width %d >= %d: %q", tc.name, cols, i, w, cols, plain)
				}
			}
		}
	}
}

func TestHybridUserEchoNoBlankLinesOnMultilineLongPrompt(t *testing.T) {
	t.Parallel()

	promptVal := "line one\n" + strings.Repeat("superlongtoken_", 12) + " end of line two"
	for _, cols := range []int{60, 80} {
		var out bytes.Buffer
		h := newStyledHybridForTest(&out)
		h.termColsOverride = cols

		echo := h.renderUserEcho("> ", promptVal)
		lines := strings.Split(echo, "\n")
		if len(lines) < 3 {
			t.Fatalf("cols=%d: expected wrapped multiline echo, got:\n%s", cols, echo)
		}
		for i, ln := range lines {
			plain := stripTestANSI(ln)
			if strings.TrimSpace(plain) == "" {
				t.Fatalf("cols=%d: phantom blank line %d in:\n%s", cols, i, echo)
			}
			if strings.TrimRight(plain, " ") != plain {
				t.Fatalf("cols=%d: line %d has trailing spaces: %q", cols, i, plain)
			}
			if !strings.HasPrefix(plain, "┃ ") {
				t.Fatalf("cols=%d: line %d missing left rail prefix: %q", cols, i, plain)
			}
			if w := displayWidth(ln); w >= cols {
				t.Fatalf("cols=%d: line %d width %d >= %d: %q", cols, i, w, cols, plain)
			}
		}
	}
}

func assertBoxFits(t *testing.T, label, rendered string, cols int) {
	t.Helper()
	trimmed := strings.Trim(rendered, "\n")
	rows := strings.Split(trimmed, "\n")
	if len(rows) < 3 {
		t.Fatalf("%s cols=%d: expected at least 3 rows, got %d:\n%s", label, cols, len(rows), rendered)
	}
	wantW := displayWidth(rows[0])
	if wantW > cols {
		t.Fatalf("%s cols=%d: box width %d > %d:\n%s", label, cols, wantW, cols, rendered)
	}
	plainFirst := stripTestANSI(rows[0])
	plainLast := stripTestANSI(rows[len(rows)-1])
	if !strings.HasPrefix(plainFirst, "╭") || !strings.HasSuffix(plainFirst, "╮") {
		t.Fatalf("%s cols=%d: bad top border %q", label, cols, plainFirst)
	}
	if !strings.HasPrefix(plainLast, "╰") || !strings.HasSuffix(plainLast, "╯") {
		t.Fatalf("%s cols=%d: bad bottom border %q", label, cols, plainLast)
	}
	for i, r := range rows {
		if w := displayWidth(r); w != wantW {
			t.Fatalf("%s cols=%d: row %d width=%d want=%d (%q)", label, cols, i, w, wantW, stripTestANSI(r))
		}
	}
}

func TestHybridBoxesFitNarrowTerminals(t *testing.T) {
	t.Parallel()

	longPath := "/mnt/backup/very/long/unbreakable/directory/structure/file_name_that_wraps.go"

	for _, cols := range []int{30, 40, 60, 80} {
		// 1. Todo panel
		var todoOut bytes.Buffer
		hTodo := newStyledHybridForTest(&todoOut)
		hTodo.termColsOverride = cols
		hTodo.ShowTodos([]TodoItem{
			{ID: "1", Content: "Inspect " + longPath, Status: todoStatusInProgress},
			{ID: "2", Content: "Done item", Status: todoStatusCompleted},
		})
		assertBoxFits(t, "Todos", todoOut.String(), cols)

		// 2. Thinking block
		var thinkOut bytes.Buffer
		hThink := newStyledHybridForTest(&thinkOut)
		hThink.termColsOverride = cols
		hThink.StreamThinking("Inspecting " + longPath + " and thinking about wrapping.\n")
		hThink.EndThinking()
		finalFrame := thinkOut.String()
		if idx := strings.LastIndex(finalFrame, "\x1b[J"); idx >= 0 {
			finalFrame = finalFrame[idx+len("\x1b[J"):]
		}
		assertBoxFits(t, "ThinkingBox", finalFrame, cols)

		// 3. Thinking viewer
		viewer := newHybridThinkingViewerModel("Trace: "+longPath+"\nSecond line", cols, 14)
		assertBoxFits(t, "ThinkingViewer", viewer.View(), cols)

		// 4. Permission card
		perm := newHybridPermissionModel([]string{
			"TARGET Bash",
			"GRAVITY HIGH",
			"WHY: " + longPath,
			"command: cat " + longPath,
		}, 10*time.Second, cols)
		assertBoxFits(t, "PermissionCard", perm.View(), cols)

		// 5. Ask card
		ask := newHybridAskModel(AskCard{
			Title:  "Choose path for " + longPath,
			Prompt: "Select destination " + longPath,
			Note:   "Note: " + longPath,
			Options: []AskOption{
				{Label: "Option One", Description: "writes to " + longPath},
				{Label: "Option Two", Description: "short"},
			},
		}, 10*time.Second, cols)
		ask.customMode = true
		ask.customBuf = []rune("custom_" + longPath)
		assertBoxFits(t, "AskCard", ask.View(), cols)

		// 6. Composer (empty placeholder, typed text + slash suggestions + notice + wide prompt)
		cmds := []SlashCommand{
			{Name: "/providers", Description: "Configure providers and endpoints for " + longPath},
			{Name: "/status", Description: "Show session status"},
		}
		inEmpty := newHybridInputModel(">>> ", StatusInfo{
			Model:         "qwen3-coder:30b-instruct",
			ContextPct:    82,
			TodosDone:     1,
			TodosTotal:    3,
			MissionActive: true,
			Offline:       true,
			SessionMode:   "isolated",
		}, nil, cmds, cols).withThinkingAvailable(true)
		for i, row := range strings.Split(inEmpty.View(), "\n") {
			if w := displayWidth(row); w > cols {
				t.Fatalf("InputEmpty cols=%d row %d width=%d > %d (%q)", cols, i, w, cols, stripTestANSI(row))
			}
		}

		inSlash := newHybridInputModel(">>> ", StatusInfo{Model: "qwen3:8b", ContextPct: 12}, nil, cmds, cols).
			withDraft([]rune("/"), 1)
		inSlash.notice = "Notice: " + longPath
		for i, row := range strings.Split(inSlash.View(), "\n") {
			if w := displayWidth(row); w > cols {
				t.Fatalf("InputSlash cols=%d row %d width=%d > %d (%q)", cols, i, w, cols, stripTestANSI(row))
			}
		}

		inTyped := newHybridInputModel(">>> ", StatusInfo{Model: "qwen3:8b"}, nil, nil, cols).
			withDraft([]rune("check "+longPath), len("check "+longPath))
		for i, row := range strings.Split(inTyped.View(), "\n") {
			if w := displayWidth(row); w > cols {
				t.Fatalf("InputTyped cols=%d row %d width=%d > %d (%q)", cols, i, w, cols, stripTestANSI(row))
			}
		}
	}
}

func TestTruncateInlineSanitizesTabs(t *testing.T) {
	t.Parallel()
	got := truncateInline("cmd\t--flag\nnext\rval", 50)
	if strings.ContainsAny(got, "\t\n\r") {
		t.Fatalf("expected tabs and newlines stripped, got %q", got)
	}
	if got != "cmd --flag next val" {
		t.Fatalf("unexpected truncateInline result: %q", got)
	}
}
