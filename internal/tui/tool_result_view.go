package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// toolResultSummary builds the text after "→" for ShowToolResult, Cursor-style
// for Write/Edit when toolParams is non-nil.
func toolResultSummary(st Style, name, output string, isError bool, toolParams map[string]any) string {
	if isError {
		return summarizeOutput(output)
	}
	if name == "AskUserQuestion" {
		if s := askUserQuestionSummary(st, output, toolParams); s != "" {
			return s
		}
	}
	if (name == "BackgroundRun" || name == "BackgroundStatus") && strings.Contains(output, "STILL RUNNING") {
		return backgroundTaskSummary(output)
	}
	chip := cursorEditWriteChip(st, name, toolParams)
	base := strings.TrimSpace(summarizeOutput(output))
	if chip != "" {
		if boringToolSuccessMessage(base) {
			return chip
		}
		return chip + st.Dim(" · ") + base
	}
	return summarizeOutput(output)
}

func askUserQuestionSummary(st Style, output string, toolParams map[string]any) string {
	if toolParams == nil {
		return ""
	}
	var answers map[string]any
	if err := json.Unmarshal([]byte(output), &answers); err != nil || len(answers) == 0 {
		return ""
	}

	rawQuestions, ok := toolParams["questions"].([]any)
	if (!ok || len(rawQuestions) == 0) && toolParams["options"] != nil {
		prompt := toolParams["prompt"]
		if prompt == nil {
			prompt = toolParams["question"]
		}
		id := toolParams["id"]
		if id == nil || strings.TrimSpace(fmt.Sprintf("%v", id)) == "" {
			id = "q1"
		}
		rawQuestions = []any{
			map[string]any{
				"id":      fmt.Sprintf("%v", id),
				"prompt":  prompt,
				"options": toolParams["options"],
			},
		}
	} else if !ok || len(rawQuestions) == 0 {
		return ""
	}

	var parts []string
	for _, qAny := range rawQuestions {
		qMap, ok := qAny.(map[string]any)
		if !ok {
			continue
		}
		qID, _ := qMap["id"].(string)
		ans, hasAns := answers[qID]
		if !hasAns && len(rawQuestions) == 1 && len(answers) == 1 {
			for _, v := range answers {
				ans = v
				hasAns = true
				break
			}
		}
		if !hasAns {
			continue
		}

		ansStr := strings.TrimSpace(fmt.Sprintf("%v", ans))
		rawOptions, _ := qMap["options"].([]any)

		chosenLabel := ""
		chosenNum := 0
		for i, optAny := range rawOptions {
			switch opt := optAny.(type) {
			case string:
				if ansStr == strconv.Itoa(i+1) || strings.EqualFold(ansStr, opt) {
					chosenLabel = opt
					chosenNum = i + 1
					break
				}
			case map[string]any:
				optID, _ := opt["id"].(string)
				optLabel, _ := opt["label"].(string)
				if (optID != "" && strings.EqualFold(ansStr, optID)) ||
					ansStr == strconv.Itoa(i+1) ||
					(optLabel != "" && strings.EqualFold(ansStr, optLabel)) {
					chosenLabel = optLabel
					chosenNum = i + 1
					break
				}
			}
			if chosenLabel != "" {
				break
			}
		}

		label := chosenLabel
		if label == "" {
			label = ansStr
		}

		prefix := ""
		if chosenNum > 0 {
			prefix = st.BrightGreen(fmt.Sprintf("[%d] ", chosenNum))
		}

		item := prefix + label
		if len(rawQuestions) > 1 && qID != "" {
			item = st.Dim(qID+": ") + item
		}
		parts = append(parts, item)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, st.Dim(" · "))
}

func backgroundTaskSummary(output string) string {
	taskID := ""
	elapsed := ""
	pid := ""
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[task_id:") {
			taskID = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "[task_id:"), "]"))
		} else if strings.HasPrefix(trimmed, "[elapsed:") {
			elapsed = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "[elapsed:"), "]"))
		} else if strings.HasPrefix(trimmed, "[pid:") {
			pid = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "[pid:"), "]"))
		}
	}
	if taskID != "" {
		res := taskID + ": still executing in background"
		if elapsed != "" {
			res += " (elapsed " + elapsed + ")"
		}
		if pid != "" {
			res += " [pid " + pid + "]"
		}
		return res
	}
	return "task is running in background (not finished yet)"
}

func boringToolSuccessMessage(s string) bool {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "", "write successful.", "ok", "done.":
		return true
	default:
		return false
	}
}

func cursorEditWriteChip(st Style, name string, params map[string]any) string {
	if params == nil {
		return ""
	}
	switch name {
	case "Write", "NotebookEdit":
		c, ok := params["contents"].(string)
		if !ok {
			return ""
		}
		n := lineCount(c)
		if n == 0 && len(c) == 0 {
			return st.Dim("empty file")
		}
		if n == 0 {
			n = 1
		}
		return st.BrightGreen("+") + st.Green(fmt.Sprintf("%d", n)) + " " + st.Dim("lines") +
			" · " + st.Dim(FormatBytes(len(c)))
	case "Edit":
		oldS, _ := params["old_string"].(string)
		newS, _ := params["new_string"].(string)
		if oldS == "" && newS == "" {
			return ""
		}
		lo, ln := lineCount(oldS), lineCount(newS)
		delta := len(newS) - len(oldS)
		deltaStr := ""
		if delta != 0 {
			sign := "+"
			d := delta
			if delta < 0 {
				sign = "−"
				d = -delta
			}
			deltaStr = " · " + st.Dim(sign+FormatBytes(d))
		}
		return st.BrightGreen("+") + st.Green(fmt.Sprintf("%d", ln)) + " " +
			st.Red("−") + st.Red(fmt.Sprintf("%d", lo)) + " " + st.Dim("lines") + deltaStr
	default:
		return ""
	}
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// printEditDiffPreview prints a colored unified diff after Edit when _diff is
// present; otherwise falls back to the 2-line Cursor-like snapshot.
func printEditDiffPreview(w io.Writer, st Style, params map[string]any) {
	if params == nil {
		return
	}
	if diff, ok := params["_diff"].(string); ok && diff != "" {
		printColoredDiff(w, st, diff)
		return
	}
	oldS, ok1 := params["old_string"].(string)
	newS, ok2 := params["new_string"].(string)
	if !ok1 || !ok2 {
		return
	}
	if strings.TrimSpace(oldS) == "" && strings.TrimSpace(newS) == "" {
		return
	}
	fp, _ := params["file_path"].(string)
	if fp != "" {
		fmt.Fprintf(w, "  %s %s\n", st.Dim("∷"), st.Dim(compactPath(fp)))
	}
	ol := firstLineOrSummary(oldS, 96)
	nl := firstLineOrSummary(newS, 96)
	if ol != "" {
		fmt.Fprintf(w, "  %s %s\n", st.Red("−"), st.Dim(ol))
	}
	if nl != "" {
		fmt.Fprintf(w, "  %s %s\n", st.Green("+"), st.Dim(nl))
	}
	if strings.Count(oldS, "\n")+strings.Count(newS, "\n") > 2 {
		fmt.Fprintf(w, "  %s\n", st.Dim("… multiline change (see file)"))
	}
}

func printColoredDiff(w io.Writer, st Style, diff string) {
	lines := strings.Split(diff, "\n")
	if len(lines) > 50 {
		lines = lines[:50]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "@@"):
			fmt.Fprintf(w, "  %s\n", st.Cyan(line))
		case strings.HasPrefix(line, "-"):
			fmt.Fprintf(w, "  %s\n", st.Red(line))
		case strings.HasPrefix(line, "+"):
			fmt.Fprintf(w, "  %s\n", st.Green(line))
		default:
			fmt.Fprintf(w, "  %s\n", st.Dim(line))
		}
	}
	if strings.Count(diff, "\n") >= 50 {
		fmt.Fprintf(w, "  %s\n", st.Dim("… diff truncated"))
	}
}

func compactPathDisplay(p string) string {
	return compactPath(p)
}

func firstLineOrSummary(s string, maxRunes int) string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return ""
	}
	line := s
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		line = s[:i]
	}
	return truncateRunes(line, maxRunes)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) > max-1 {
		return string(r[:max-1]) + "…"
	}
	return s
}
