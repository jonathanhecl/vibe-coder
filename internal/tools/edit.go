package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type EditTool struct{}

func NewEditTool() *EditTool { return &EditTool{} }

func unifiedDiff(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	i := 0
	for i < len(al) && i < len(bl) && al[i] == bl[i] {
		i++
	}
	j, k := len(al)-1, len(bl)-1
	for j >= i && k >= i && al[j] == bl[k] {
		j--
		k--
	}
	if i > j && i > k {
		return ""
	}
	const c = 3
	lo := max(0, i-c)
	ro := min(len(al), j+1+c)
	rb := min(len(bl), k+1+c)
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", lo+1, ro-lo, lo+1, rb-lo)
	o, n := lo, lo
	for o < ro || n < rb {
		if o < ro && n < rb && al[o] == bl[n] && (o < i || o > j) {
			fmt.Fprintf(&sb, " %s\n", al[o])
			o++
			n++
			continue
		}
		if o <= j && o < ro {
			fmt.Fprintf(&sb, "-%s\n", al[o])
			o++
		} else if n <= k && n < rb {
			fmt.Fprintf(&sb, "+%s\n", bl[n])
			n++
		} else if o < ro {
			fmt.Fprintf(&sb, " %s\n", al[o])
			o++
			n++
		} else {
			fmt.Fprintf(&sb, "+%s\n", bl[n])
			n++
		}
	}
	return sb.String()
}

// lineSeparator returns the dominant newline sequence in s.
func lineSeparator(s string) string {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n") - crlf
	if crlf > lf {
		return "\r\n"
	}
	return "\n"
}

// normalizeNewString adapts new_string's line endings to match the file's dominant style.
func normalizeNewString(newString, sep string) string {
	if sep == "\n" {
		return newString
	}
	lines := strings.Split(newString, "\n")
	return strings.Join(lines, sep)
}

// editSplitLines returns the lines of s using \n as separator, regardless of actual line endings.
func editSplitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// replaceLineRange replaces lines [startLine, endLine] (1-based, inclusive) with newString.
func replaceLineRange(content, newString string, startLine, endLine int) (string, error) {
	if startLine < 1 {
		return "", fmt.Errorf("start_line must be >= 1")
	}
	if endLine < startLine {
		endLine = startLine
	}
	sep := lineSeparator(content)
	lines := editSplitLines(content)
	if startLine > len(lines) {
		return "", fmt.Errorf("start_line %d out of range (file has %d lines)", startLine, len(lines))
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	newString = normalizeNewString(newString, sep)
	parts := make([]string, 0, len(lines)-endLine+startLine+1)
	for i, line := range lines {
		ln := i + 1
		if ln == startLine {
			parts = append(parts, newString)
		}
		if ln < startLine || ln > endLine {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, sep), nil
}

// findMatches returns the start indices of every non-overlapping occurrence of substr in s.
func findMatches(s, substr string) []int {
	if substr == "" {
		return nil
	}
	var matches []int
	start := 0
	for {
		idx := strings.Index(s[start:], substr)
		if idx < 0 {
			break
		}
		pos := start + idx
		matches = append(matches, pos)
		start = pos + len(substr)
	}
	return matches
}

// normalizeCRLF returns s with every \r\n replaced by \n, plus a length map so that
// positions in the normalized string can be translated back to the original string.
// lengths[i] is the number of bytes in the original string that produced normalized[i].
func normalizeCRLF(s string) (string, []int) {
	var b strings.Builder
	lengths := make([]int, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			b.WriteByte('\n')
			lengths = append(lengths, 2)
			i += 2
		} else {
			b.WriteByte(s[i])
			lengths = append(lengths, 1)
			i++
		}
	}
	return b.String(), lengths
}

// replaceCRLFMatch applies a replacement discovered in CRLF-normalized space back to the
// original content so that only the matched spans are changed and the rest of the file
// keeps its original line endings.
func replaceCRLFMatch(content, oldNorm, newString string, matches []int, lengths []int) string {
	if len(matches) == 0 {
		return content
	}
	prefix := make([]int, len(lengths)+1)
	for i, l := range lengths {
		prefix[i+1] = prefix[i] + l
	}
	sep := lineSeparator(content)
	newNorm := normalizeNewString(newString, sep)
	var b strings.Builder
	last := 0
	for _, m := range matches {
		startOrig := prefix[m]
		endOrig := prefix[m+len(oldNorm)]
		b.WriteString(content[last:startOrig])
		b.WriteString(newNorm)
		last = endOrig
	}
	b.WriteString(content[last:])
	return b.String()
}

// replaceTrimmedMatch searches for oldString after stripping surrounding whitespace in
// content normalized to LF. It replaces every match with newString and then converts
// the whole result back to the file's dominant line separator. This is a recovery path,
// so normalizing the entire file's line endings is acceptable.
func replaceTrimmedMatch(content, oldString, newString string, replaceAll bool) (string, int) {
	sep := lineSeparator(content)
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	trimOld := strings.TrimSpace(oldString)
	matches := findMatches(norm, trimOld)
	if len(matches) == 0 {
		return content, 0
	}
	if len(matches) > 1 && !replaceAll {
		return content, len(matches)
	}
	limit := 1
	if replaceAll {
		limit = -1
	}
	updated := strings.Replace(norm, trimOld, newString, limit)
	if sep == "\r\n" {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	return updated, len(matches)
}

func (t *EditTool) Name() string { return "Edit" }
func (t *EditTool) Description() string {
	return "Edit file content by replacing exact text or a line range."
}
func (t *EditTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path":   map[string]any{"type": "string"},
					"old_string":  map[string]any{"type": "string"},
					"new_string":  map[string]any{"type": "string"},
					"replace_all": map[string]any{"type": "boolean"},
					"start_line":  map[string]any{"type": "integer"},
					"end_line":    map[string]any{"type": "integer"},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, params map[string]any) Result {
	path, ok := params["file_path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return errResult("file_path is required")
	}
	oldString, _ := params["old_string"].(string)
	newString, ok := params["new_string"].(string)
	if !ok {
		return errResult("new_string must be string")
	}
	replaceAll, _ := params["replace_all"].(bool)
	startLine := asInt(params["start_line"], 0)
	endLine := asInt(params["end_line"], 0)

	path = strings.TrimSpace(path)
	vr := validateExistingFileForRead(path)
	if vr.IsError() {
		return Result{Output: vr.UserError, HintsForModel: vr.AssistantHints, IsError: true}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Output: agentPathPreamble(fmt.Sprintf("read file: %v", err)), HintsForModel: assistantPathHints(path, "read", err), IsError: true}
	}
	content := string(data)

	// Line-range mode takes precedence and does not require old_string matching.
	if startLine > 0 {
		updated, err := replaceLineRange(content, newString, startLine, endLine)
		if err != nil {
			return errResult(err.Error())
		}
		return applyEdit(ctx, path, content, updated)
	}

	if oldString == "" {
		return errResult("old_string is required when start_line is not provided")
	}

	// 1. Exact match on raw content.
	matches := findMatches(content, oldString)
	if len(matches) > 0 {
		return t.replaceAndWrite(ctx, path, content, oldString, newString, replaceAll, matches)
	}

	// 2. Exact match after normalizing CRLF -> LF, mapped back to original content.
	// Only attempt this path when there is a real line-ending mismatch; otherwise
	// accidental whitespace/newlines in old_string would be treated as an exact match.
	if strings.Contains(content, "\r\n") || strings.Contains(oldString, "\r\n") {
		normContent, lengths := normalizeCRLF(content)
		normOld := strings.ReplaceAll(oldString, "\r\n", "\n")
		matches = findMatches(normContent, normOld)
		if len(matches) > 0 {
			updated := replaceCRLFMatch(content, normOld, newString, matches, lengths)
			return applyEdit(ctx, path, content, updated)
		}
	}

	// 3. Trimmed whitespace fallback (recovery path; may normalize file line endings).
	updated, trimMatchCount := replaceTrimmedMatch(content, oldString, newString, replaceAll)
	if trimMatchCount > 0 {
		if trimMatchCount > 1 && !replaceAll {
			return errResult("old_string matched multiple times, set replace_all=true")
		}
		return applyEdit(ctx, path, content, updated)
	}

	hints := "Hints: copy old_string from the file content, not from the numbered Read output; " +
		"check for CRLF/LF mismatches or extra surrounding whitespace; " +
		"for whole-line changes use start_line/end_line instead."
	return Result{
		Output:        "old_string not found",
		HintsForModel: hints,
		IsError:       true,
	}
}

func (t *EditTool) replaceAndWrite(ctx context.Context, path, content, oldString, newString string, replaceAll bool, matches []int) Result {
	if len(matches) > 1 && !replaceAll {
		return errResult("old_string matched multiple times, set replace_all=true")
	}
	limit := 1
	if replaceAll {
		limit = -1
	}
	updated := strings.Replace(content, oldString, newString, limit)
	return applyEdit(ctx, path, content, updated)
}

func applyEdit(ctx context.Context, path, original, updated string) Result {
	res := NewWriteTool().Execute(ctx, map[string]any{
		"file_path": path,
		"contents":  updated,
	})
	res.Diff = unifiedDiff(original, updated)
	return res
}
