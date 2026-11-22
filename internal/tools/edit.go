package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type EditTool struct{}

func NewEditTool() *EditTool { return &EditTool{} }

var readLineNumberPrefixRE = regexp.MustCompile(`^[ \t]*\d+\|`)

const (
	editLineWindowSlack     = 15
	fuzzyMinMeanSimilarity  = 0.85
	fuzzyMinEdgeSimilarity  = 0.80
	fuzzyUniquenessMargin   = 0.08
	fuzzyMinNonEmptyChars   = 12
	hintMinSimilarity       = 0.35
	hintMaxSnippetLines     = 12
	hintMaxSnippetBytes     = 1500
	hybridRangeMinSim       = 0.55
)

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
	norm := strings.ReplaceAll(newString, "\r\n", "\n")
	if sep == "\n" {
		return norm
	}
	return strings.ReplaceAll(norm, "\n", sep)
}

// editSplitLines returns the lines of s using \n as separator, regardless of actual line endings.
func editSplitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// splitBlockLines splits a replacement snippet into lines, dropping at most one
// trailing empty element created by a terminating newline so multi-line snippets
// ending with "\n" still match the exact block lines in the file.
func splitBlockLines(s string) []string {
	norm := strings.ReplaceAll(s, "\r\n", "\n")
	norm = strings.TrimSuffix(norm, "\n")
	return strings.Split(norm, "\n")
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

// stripReadLineNumberPrefixes strips Read's "     5|" prefix when every
// non-empty line in s carries that format.
func stripReadLineNumberPrefixes(s string) (string, bool) {
	norm := strings.ReplaceAll(s, "\r\n", "\n")
	hadTrailingNL := strings.HasSuffix(norm, "\n")
	lines := strings.Split(strings.TrimSuffix(norm, "\n"), "\n")
	strippedCount := 0
	for i, line := range lines {
		if line == "" {
			continue
		}
		loc := readLineNumberPrefixRE.FindStringIndex(line)
		if loc == nil {
			return s, false
		}
		lines[i] = line[loc[1]:]
		strippedCount++
	}
	if strippedCount == 0 {
		return s, false
	}
	out := strings.Join(lines, "\n")
	if hadTrailingNL {
		out += "\n"
	}
	return out, true
}

func leadingHorizontalWS(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return line[:i]
		}
	}
	return line
}

func firstNonEmptyLine(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

func usesTabs(lines []string) bool {
	for _, l := range lines {
		ws := leadingHorizontalWS(l)
		if strings.ContainsRune(ws, '\t') {
			return true
		}
	}
	return false
}

func detectSpaceIndentUnit(lines []string, baseIndent string) int {
	best := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		rel := strings.TrimPrefix(l, baseIndent)
		ws := leadingHorizontalWS(rel)
		if len(ws) > 0 && !strings.ContainsRune(ws, '\t') {
			if best == 0 || len(ws) < best {
				best = len(ws)
			}
		}
	}
	if best == 2 || best == 4 {
		return best
	}
	if best > 4 {
		return 4
	}
	return 4
}

func convertLeadingSpacesToTabs(rel string, unit int) string {
	if unit <= 0 {
		return rel
	}
	ws := leadingHorizontalWS(rel)
	if ws == "" || strings.ContainsRune(ws, '\t') {
		return rel
	}
	rest := rel[len(ws):]
	tabs := len(ws) / unit
	rem := len(ws) % unit
	return strings.Repeat("\t", tabs) + strings.Repeat(" ", rem) + rest
}

// reindentReplacement aligns newLines to matchedFileLines when oldLines had
// missing or mismatched base indentation (or spaces vs tabs relative indent).
func reindentReplacement(matchedFileLines, oldLines, newLines []string) []string {
	fileFirst := firstNonEmptyLine(matchedFileLines)
	oldFirst := firstNonEmptyLine(oldLines)
	newFirst := firstNonEmptyLine(newLines)
	if fileFirst == "" || oldFirst == "" || newFirst == "" {
		return append([]string(nil), newLines...)
	}
	fileBase := leadingHorizontalWS(fileFirst)
	oldBase := leadingHorizontalWS(oldFirst)
	newBase := leadingHorizontalWS(newFirst)
	if fileBase == oldBase {
		return append([]string(nil), newLines...)
	}
	fileTabbed := usesTabs(matchedFileLines)
	oldTabbed := usesTabs(oldLines)
	spaceUnit := detectSpaceIndentUnit(oldLines, oldBase)

	out := make([]string, len(newLines))
	for i, line := range newLines {
		if strings.TrimSpace(line) == "" {
			out[i] = ""
			continue
		}
		var rel string
		switch {
		case oldBase == "" && newBase == "":
			rel = line
		case oldBase != "" && strings.HasPrefix(line, oldBase):
			rel = line[len(oldBase):]
		case newBase == "" && fileBase != "":
			rel = line
		case newBase != "" && strings.HasPrefix(line, newBase) && len(newBase) >= len(oldBase):
			// Model kept its own base indent in newLines different from oldBase; preserve delta when possible.
			out[i] = line
			continue
		default:
			ws := leadingHorizontalWS(line)
			if len(ws) >= len(oldBase) {
				rel = line[len(oldBase):]
			} else {
				rel = strings.TrimLeft(line, " \t")
			}
		}
		if fileTabbed && !oldTabbed {
			rel = convertLeadingSpacesToTabs(rel, spaceUnit)
		}
		out[i] = fileBase + rel
	}
	return out
}

type lineSpan struct {
	start int // 0-based inclusive line index in fileLines
	end   int // 0-based exclusive line index in fileLines
	score float64
}

func replaceLineSpans(fileLines []string, spans []lineSpan, oldLines, newLines []string, sep string) string {
	// Spans must be sorted ascending and non-overlapping.
	var out []string
	cursor := 0
	for _, sp := range spans {
		if sp.start < cursor || sp.end > len(fileLines) {
			continue
		}
		out = append(out, fileLines[cursor:sp.start]...)
		reindented := reindentReplacement(fileLines[sp.start:sp.end], oldLines, newLines)
		out = append(out, reindented...)
		cursor = sp.end
	}
	out = append(out, fileLines[cursor:]...)
	return strings.Join(out, sep)
}

func trimOuterBlankLines(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func findLineTrimmedSpans(fileLines, oldLines []string) []lineSpan {
	if len(oldLines) == 0 || len(fileLines) < len(oldLines) {
		return nil
	}
	trimmedOld := make([]string, len(oldLines))
	hasNonEmpty := false
	for i, l := range oldLines {
		trimmedOld[i] = strings.TrimSpace(l)
		if trimmedOld[i] != "" {
			hasNonEmpty = true
		}
	}
	if !hasNonEmpty {
		return nil
	}

	var spans []lineSpan
	for i := 0; i <= len(fileLines)-len(oldLines); {
		matched := true
		for j := 0; j < len(oldLines); j++ {
			if strings.TrimSpace(fileLines[i+j]) != trimmedOld[j] {
				matched = false
				break
			}
		}
		if matched {
			spans = append(spans, lineSpan{start: i, end: i + len(oldLines), score: 1.0})
			i += len(oldLines)
			continue
		}
		i++
	}
	if len(spans) > 0 {
		return spans
	}

	// Blank-line-tolerant fallback for multi-line blocks: compare non-empty trimmed lines.
	var oldCompact []string
	for _, l := range trimmedOld {
		if l != "" {
			oldCompact = append(oldCompact, l)
		}
	}
	if len(oldCompact) < 2 {
		return nil
	}
	for i := 0; i < len(fileLines); {
		if strings.TrimSpace(fileLines[i]) != oldCompact[0] {
			i++
			continue
		}
		fi := i
		oi := 0
		for fi < len(fileLines) && oi < len(oldCompact) {
			tf := strings.TrimSpace(fileLines[fi])
			if tf == "" {
				fi++
				continue
			}
			if tf != oldCompact[oi] {
				break
			}
			fi++
			oi++
		}
		if oi == len(oldCompact) {
			spans = append(spans, lineSpan{start: i, end: fi, score: 1.0})
			i = fi
			continue
		}
		i++
	}
	return spans
}

func lineSimilarity(a, b string) float64 {
	ta := strings.TrimSpace(a)
	tb := strings.TrimSpace(b)
	if ta == "" && tb == "" {
		return 1.0
	}
	if ta == "" || tb == "" {
		return 0.0
	}
	if ta == tb {
		return 1.0
	}
	ra := []rune(ta)
	rb := []rune(tb)
	maxLen := max(len(ra), len(rb))
	dist := levenshteinRunes(ra, rb)
	return 1.0 - float64(dist)/float64(maxLen)
}

func levenshteinRunes(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func blockSimilarity(fileSlice, oldLines []string) (mean float64, firstEdge float64, lastEdge float64) {
	if len(fileSlice) != len(oldLines) || len(oldLines) == 0 {
		return 0, 0, 0
	}
	firstOldIdx, lastOldIdx := -1, -1
	weightedSum := 0.0
	totalWeight := 0.0
	for i := range oldLines {
		sim := lineSimilarity(fileSlice[i], oldLines[i])
		la := len([]rune(strings.TrimSpace(fileSlice[i])))
		lb := len([]rune(strings.TrimSpace(oldLines[i])))
		w := float64(max(la, lb))
		if w < 1 {
			w = 1
		}
		weightedSum += sim * w
		totalWeight += w
		if strings.TrimSpace(oldLines[i]) != "" {
			if firstOldIdx < 0 {
				firstOldIdx = i
			}
			lastOldIdx = i
		}
	}
	if totalWeight > 0 {
		mean = weightedSum / totalWeight
	}
	if firstOldIdx >= 0 {
		firstEdge = lineSimilarity(fileSlice[firstOldIdx], oldLines[firstOldIdx])
	}
	if lastOldIdx >= 0 {
		lastEdge = lineSimilarity(fileSlice[lastOldIdx], oldLines[lastOldIdx])
	}
	return mean, firstEdge, lastEdge
}

func findFuzzyBlockSpans(fileLines, oldLines []string) []lineSpan {
	if len(oldLines) < 2 || len(fileLines) < len(oldLines) {
		return nil
	}
	nonEmptyCount := 0
	nonEmptyChars := 0
	for _, l := range oldLines {
		t := strings.TrimSpace(l)
		if t != "" {
			nonEmptyCount++
			nonEmptyChars += len([]rune(t))
		}
	}
	if nonEmptyCount < 2 || nonEmptyChars < fuzzyMinNonEmptyChars {
		return nil
	}

	var candidates []lineSpan
	for i := 0; i <= len(fileLines)-len(oldLines); i++ {
		mean, firstEdge, lastEdge := blockSimilarity(fileLines[i:i+len(oldLines)], oldLines)
		if firstEdge >= fuzzyMinEdgeSimilarity && lastEdge >= fuzzyMinEdgeSimilarity && mean >= fuzzyMinMeanSimilarity {
			candidates = append(candidates, lineSpan{
				start: i,
				end:   i + len(oldLines),
				score: mean,
			})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Deduplicate overlapping windows greedily by highest score first.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].start < candidates[j].start
	})
	var chosen []lineSpan
	for _, c := range candidates {
		overlaps := false
		for _, k := range chosen {
			if c.start < k.end && c.end > k.start {
				overlaps = true
				break
			}
		}
		if !overlaps {
			chosen = append(chosen, c)
		}
	}
	return chosen
}

// matchStartLine converts a byte offset in text to a 1-based line number.
func matchStartLine(s string, byteOffset int) int {
	if byteOffset <= 0 {
		return 1
	}
	if byteOffset > len(s) {
		byteOffset = len(s)
	}
	return 1 + strings.Count(s[:byteOffset], "\n")
}

// filterSpansByLineWindow keeps spans overlapping [minLine, maxLine] (1-based inclusive).
func filterSpansByLineWindow(spans []lineSpan, minLine, maxLine int) []lineSpan {
	var out []lineSpan
	for _, sp := range spans {
		spStart := sp.start + 1
		spEnd := sp.end
		if spEnd < spStart {
			spEnd = spStart
		}
		if spEnd >= minLine && spStart <= maxLine {
			out = append(out, sp)
		}
	}
	return out
}

// pickClosestSpan chooses the span closest to targetLine (1-based). Returns ok=false if tied.
func pickClosestSpan(spans []lineSpan, targetLine int) (lineSpan, bool) {
	if len(spans) == 0 {
		return lineSpan{}, false
	}
	if len(spans) == 1 {
		return spans[0], true
	}
	bestIdx := 0
	bestDist := absInt((spans[0].start + 1) - targetLine)
	tied := false
	for i := 1; i < len(spans); i++ {
		d := absInt((spans[i].start + 1) - targetLine)
		if d < bestDist {
			bestDist = d
			bestIdx = i
			tied = false
		} else if d == bestDist {
			tied = true
		}
	}
	if tied {
		return lineSpan{}, false
	}
	return spans[bestIdx], true
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// replaceTrimmedMatch searches for oldString after stripping surrounding whitespace in
// content normalized to LF (inline single-line fallback).
func replaceTrimmedMatch(content, oldString, newString string, replaceAll bool) (string, int) {
	sep := lineSeparator(content)
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	trimOld := strings.TrimSpace(oldString)
	if trimOld == "" {
		return content, 0
	}
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
	updated := strings.Replace(norm, trimOld, strings.ReplaceAll(newString, "\r\n", "\n"), limit)
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
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to the existing file to edit.",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "Text to replace (optional when start_line is provided).",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "Replacement text.",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "Replace all occurrences instead of failing on multiple matches.",
					},
					"start_line": map[string]any{
						"type":        "integer",
						"description": "1-based start line for line-range replacement or disambiguating old_string.",
					},
					"end_line": map[string]any{
						"type":        "integer",
						"description": "1-based inclusive end line for line-range replacement (defaults to start_line).",
					},
				},
				"required": []string{"file_path", "new_string"},
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

	// Pure line-range mode when old_string is empty.
	if startLine > 0 && strings.TrimSpace(oldString) == "" {
		updated, err := replaceLineRange(content, newString, startLine, endLine)
		if err != nil {
			return errResult(err.Error())
		}
		return applyEdit(ctx, path, content, updated)
	}

	if oldString == "" {
		return errResult("old_string is required when start_line is not provided")
	}

	fileLines := editSplitLines(content)
	if startLine > 0 && startLine > len(fileLines) {
		return errResult(fmt.Sprintf("start_line %d out of range (file has %d lines)", startLine, len(fileLines)))
	}

	// Build candidate pairs: original (oldString, newString), plus a Read-prefix-stripped
	// variant if the model accidentally copied "     5|..." prefixes.
	type editPair struct {
		oldStr string
		newStr string
	}
	candidates := []editPair{{oldStr: oldString, newStr: newString}}
	if strippedOld, ok := stripReadLineNumberPrefixes(oldString); ok && strippedOld != oldString {
		strippedNew := newString
		if sNew, okNew := stripReadLineNumberPrefixes(newString); okNew {
			strippedNew = sNew
		}
		candidates = append(candidates, editPair{oldStr: strippedOld, newStr: strippedNew})
	}

	if startLine > 0 {
		anchorEnd := max(startLine, endLine)
		if anchorEnd > len(fileLines) {
			anchorEnd = len(fileLines)
		}
		winMin := max(1, startLine-editLineWindowSlack)
		winMax := min(len(fileLines), anchorEnd+editLineWindowSlack)

		// 1. Try scoped match around [startLine, anchorEnd] so supplying both
		// start_line and old_string disambiguates or locates old_string without
		// accidentally wiping unrelated lines in the range.
		for _, cand := range candidates {
			if res, handled := t.tryReplaceWithCandidates(ctx, path, content, fileLines, cand.oldStr, cand.newStr, replaceAll, winMin, winMax, startLine); handled {
				return res
			}
		}

		// 2. If old_string roughly matches the lines at [startLine, anchorEnd]
		// (model provided the range + an imperfect copy of those lines), replace
		// the explicit range safely.
		rangeSlice := fileLines[startLine-1 : anchorEnd]
		for _, cand := range candidates {
			oldLines := splitBlockLines(cand.oldStr)
			if len(oldLines) == len(rangeSlice) {
				mean, _, _ := blockSimilarity(rangeSlice, oldLines)
				if mean >= hybridRangeMinSim {
					updated, err := replaceLineRange(content, cand.newStr, startLine, anchorEnd)
					if err == nil {
						return applyEdit(ctx, path, content, updated)
					}
				}
			} else if len(rangeSlice) > 0 && len(oldLines) > 0 {
				mean := lineSimilarity(strings.Join(rangeSlice, " "), strings.Join(oldLines, " "))
				if mean >= hybridRangeMinSim {
					updated, err := replaceLineRange(content, cand.newStr, startLine, anchorEnd)
					if err == nil {
						return applyEdit(ctx, path, content, updated)
					}
				}
			}
		}
	}

	// Whole-file matching across all recovery tiers.
	for _, cand := range candidates {
		if res, handled := t.tryReplaceWithCandidates(ctx, path, content, fileLines, cand.oldStr, cand.newStr, replaceAll, 0, 0, 0); handled {
			return res
		}
	}

	return Result{
		Output:        "old_string not found",
		HintsForModel: buildNotFoundHints(fileLines, candidates[len(candidates)-1].oldStr, startLine, endLine),
		IsError:       true,
	}
}

// tryReplaceWithCandidates executes the tiered matching pipeline. When winMin > 0,
// matches are filtered to [winMin, winMax] and closest-line disambiguation is
// applied relative to targetLine.
func (t *EditTool) tryReplaceWithCandidates(
	ctx context.Context,
	path, content string,
	fileLines []string,
	oldString, newString string,
	replaceAll bool,
	winMin, winMax, targetLine int,
) (Result, bool) {
	scoped := winMin > 0 && winMax >= winMin
	sep := lineSeparator(content)

	// Tier 1: Exact match on raw content.
	if matches := findMatches(content, oldString); len(matches) > 0 {
		if scoped && !replaceAll {
			var inWin []int
			for _, pos := range matches {
				ln := matchStartLine(content, pos)
				if ln >= winMin && ln <= winMax {
					inWin = append(inWin, pos)
				}
			}
			if len(inWin) == 0 {
				return Result{}, false
			}
			chosen, ok := pickClosestOffset(content, inWin, targetLine)
			if !ok {
				return errResult("old_string matched multiple times near start_line, set replace_all=true"), true
			}
			updated := content[:chosen] + newString + content[chosen+len(oldString):]
			return applyEdit(ctx, path, content, updated), true
		}
		return t.replaceAndWrite(ctx, path, content, oldString, newString, replaceAll, matches), true
	}

	// Tier 2: Exact match after normalizing CRLF -> LF, mapped back to original content.
	if strings.Contains(content, "\r\n") || strings.Contains(oldString, "\r\n") {
		normContent, lengths := normalizeCRLF(content)
		normOld := strings.ReplaceAll(oldString, "\r\n", "\n")
		if matches := findMatches(normContent, normOld); len(matches) > 0 {
			if scoped && !replaceAll {
				var inWin []int
				for _, pos := range matches {
					ln := matchStartLine(normContent, pos)
					if ln >= winMin && ln <= winMax {
						inWin = append(inWin, pos)
					}
				}
				if len(inWin) == 0 {
					return Result{}, false
				}
				chosen, ok := pickClosestOffset(normContent, inWin, targetLine)
				if !ok {
					return errResult("old_string matched multiple times near start_line, set replace_all=true"), true
				}
				updated := replaceCRLFMatch(content, normOld, newString, []int{chosen}, lengths)
				return applyEdit(ctx, path, content, updated), true
			}
			if len(matches) > 1 && !replaceAll {
				return errResult("old_string matched multiple times, set replace_all=true"), true
			}
			updated := replaceCRLFMatch(content, normOld, newString, matches, lengths)
			return applyEdit(ctx, path, content, updated), true
		}
	}

	// Tier 3: Line-trimmed matching with indentation preservation.
	oldLines := splitBlockLines(oldString)
	newLines := splitBlockLines(newString)
	trimmedSpans := findLineTrimmedSpans(fileLines, oldLines)
	if len(trimmedSpans) == 0 {
		trimmedOuter := trimOuterBlankLines(oldLines)
		if len(trimmedOuter) > 0 && len(trimmedOuter) < len(oldLines) {
			oldLines = trimmedOuter
			if trimmedNew := trimOuterBlankLines(newLines); len(trimmedNew) > 0 {
				newLines = trimmedNew
			}
			trimmedSpans = findLineTrimmedSpans(fileLines, oldLines)
		}
	}
	if len(trimmedSpans) > 0 {
		if scoped && !replaceAll {
			inWin := filterSpansByLineWindow(trimmedSpans, winMin, winMax)
			if len(inWin) == 0 {
				return Result{}, false
			}
			chosen, ok := pickClosestSpan(inWin, targetLine)
			if !ok {
				return errResult("old_string matched multiple times near start_line, set replace_all=true"), true
			}
			updated := replaceLineSpans(fileLines, []lineSpan{chosen}, oldLines, newLines, sep)
			return applyEdit(ctx, path, content, updated), true
		}
		if len(trimmedSpans) > 1 && !replaceAll {
			return errResult("old_string matched multiple times, set replace_all=true"), true
		}
		sort.Slice(trimmedSpans, func(i, j int) bool { return trimmedSpans[i].start < trimmedSpans[j].start })
		updated := replaceLineSpans(fileLines, trimmedSpans, oldLines, newLines, sep)
		return applyEdit(ctx, path, content, updated), true
	}

	// Tier 4: Single-line trimmed inline substring fallback (when not scoped or inside line).
	if !scoped && !strings.Contains(strings.TrimSpace(oldString), "\n") {
		updated, trimMatchCount := replaceTrimmedMatch(content, oldString, newString, replaceAll)
		if trimMatchCount > 0 {
			if trimMatchCount > 1 && !replaceAll {
				return errResult("old_string matched multiple times, set replace_all=true"), true
			}
			return applyEdit(ctx, path, content, updated), true
		}
	}

	// Tier 5: High-confidence fuzzy multi-line block match.
	fuzzySpans := findFuzzyBlockSpans(fileLines, oldLines)
	if len(fuzzySpans) > 0 {
		if scoped && !replaceAll {
			inWin := filterSpansByLineWindow(fuzzySpans, winMin, winMax)
			if len(inWin) == 0 {
				return Result{}, false
			}
			if len(inWin) > 1 && inWin[0].score-inWin[1].score < fuzzyUniquenessMargin {
				chosen, ok := pickClosestSpan(inWin, targetLine)
				if !ok {
					return errResult("old_string matched multiple times near start_line, set replace_all=true"), true
				}
				updated := replaceLineSpans(fileLines, []lineSpan{chosen}, oldLines, newLines, sep)
				return applyEdit(ctx, path, content, updated), true
			}
			updated := replaceLineSpans(fileLines, []lineSpan{inWin[0]}, oldLines, newLines, sep)
			return applyEdit(ctx, path, content, updated), true
		}
		if len(fuzzySpans) > 1 && !replaceAll {
			if fuzzySpans[0].score-fuzzySpans[1].score < fuzzyUniquenessMargin {
				return errResult("old_string matched multiple times, set replace_all=true"), true
			}
			updated := replaceLineSpans(fileLines, []lineSpan{fuzzySpans[0]}, oldLines, newLines, sep)
			return applyEdit(ctx, path, content, updated), true
		}
		sort.Slice(fuzzySpans, func(i, j int) bool { return fuzzySpans[i].start < fuzzySpans[j].start })
		updated := replaceLineSpans(fileLines, fuzzySpans, oldLines, newLines, sep)
		return applyEdit(ctx, path, content, updated), true
	}

	return Result{}, false
}

func pickClosestOffset(s string, offsets []int, targetLine int) (int, bool) {
	if len(offsets) == 0 {
		return 0, false
	}
	if len(offsets) == 1 {
		return offsets[0], true
	}
	best := offsets[0]
	bestDist := absInt(matchStartLine(s, offsets[0]) - targetLine)
	tied := false
	for i := 1; i < len(offsets); i++ {
		d := absInt(matchStartLine(s, offsets[i]) - targetLine)
		if d < bestDist {
			bestDist = d
			best = offsets[i]
			tied = false
		} else if d == bestDist {
			tied = true
		}
	}
	if tied {
		return 0, false
	}
	return best, true
}

func buildNotFoundHints(fileLines []string, oldString string, startLine, endLine int) string {
	base := "Hints: copy old_string from the file content, not from the numbered Read output; " +
		"check for CRLF/LF mismatches or extra surrounding whitespace; " +
		"for whole-line changes use start_line/end_line instead."

	var extra strings.Builder
	if startLine > 0 && startLine <= len(fileLines) {
		anchorEnd := max(startLine, endLine)
		if anchorEnd > len(fileLines) {
			anchorEnd = len(fileLines)
		}
		lo := max(1, startLine-2)
		hi := min(len(fileLines), anchorEnd+2)
		if hi-lo+1 > hintMaxSnippetLines {
			hi = lo + hintMaxSnippetLines - 1
		}
		fmt.Fprintf(&extra, "\nActual file content at lines %d-%d:\n%s", lo, hi, formatNumberedSnippet(fileLines, lo, hi))
	}

	if lo, hi, score := findClosestSnippet(fileLines, splitBlockLines(oldString)); lo > 0 && score >= hintMinSimilarity {
		// Avoid duplicating the exact same range already printed above.
		if startLine <= 0 || hi < startLine-2 || lo > max(startLine, endLine)+2 {
			fmt.Fprintf(&extra, "\nClosest region in file (lines %d-%d, %d%% line match):\n%s",
				lo, hi, int(score*100+0.5), formatNumberedSnippet(fileLines, lo, hi))
		}
	}

	return base + extra.String()
}

func findClosestSnippet(fileLines, oldLines []string) (startLine, endLine int, bestScore float64) {
	if len(fileLines) == 0 || len(oldLines) == 0 {
		return 0, 0, 0
	}
	oldTrim := trimOuterBlankLines(oldLines)
	if len(oldTrim) == 0 {
		return 0, 0, 0
	}
	if len(oldTrim) == 1 {
		target := strings.TrimSpace(oldTrim[0])
		bestIdx := -1
		for i, line := range fileLines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			sim := lineSimilarity(line, target)
			if sim > bestScore {
				bestScore = sim
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			return 0, 0, 0
		}
		lo := max(1, bestIdx+1-2)
		hi := min(len(fileLines), bestIdx+1+2)
		return lo, hi, bestScore
	}

	winLen := len(oldTrim)
	if winLen > len(fileLines) {
		winLen = len(fileLines)
		oldTrim = oldTrim[:winLen]
	}
	if winLen > hintMaxSnippetLines {
		winLen = hintMaxSnippetLines
		oldTrim = oldTrim[:winLen]
	}
	bestIdx := -1
	for i := 0; i <= len(fileLines)-winLen; i++ {
		mean, _, _ := blockSimilarity(fileLines[i:i+winLen], oldTrim)
		if mean > bestScore {
			bestScore = mean
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return 0, 0, 0
	}
	return bestIdx + 1, bestIdx + winLen, bestScore
}

func formatNumberedSnippet(fileLines []string, startLine, endLine int) string {
	var b strings.Builder
	for ln := startLine; ln <= endLine && ln <= len(fileLines); ln++ {
		line := fmt.Sprintf("%6d|%s\n", ln, fileLines[ln-1])
		if b.Len()+len(line) > hintMaxSnippetBytes {
			b.WriteString("... (snippet truncated)\n")
			break
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n")
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
