//go:build rag

package rag

import (
	"math"
	"path/filepath"
	"strings"
)

func isIgnoredDir(name string) bool {
	switch name {
	case ".git", "node_modules", "target", "dist", "vendor", "build", ".vibe-coder":
		return true
	default:
		return false
	}
}

func isAllowedTextFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".md", ".rs", ".java", ".c", ".cpp", ".h", ".rb", ".php", ".sh", ".yaml", ".yml", ".json", ".toml":
		return true
	default:
		return false
	}
}

func splitChunks(text string, size, overlap int) []Chunk {
	if size <= 0 {
		size = 800
	}
	if overlap < 0 || overlap >= size {
		overlap = 200
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	out := make([]Chunk, 0, len(runes)/size+1)
	start := 0
	for start < len(runes) {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		part := string(runes[start:end])
		lineStart := 1 + strings.Count(string(runes[:start]), "\n")
		lineEnd := lineStart + strings.Count(part, "\n")
		out = append(out, Chunk{Start: lineStart, End: lineEnd, Text: part})
		if end == len(runes) {
			break
		}
		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return out
}

func tokenize(text string) []string {
	low := strings.ToLower(text)
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, low)
	fields := strings.Fields(clean)
	if len(fields) == 0 {
		return nil
	}
	uniq := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, ok := uniq[f]; ok {
			continue
		}
		uniq[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

func cosineOverlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := map[string]struct{}{}
	for _, x := range a {
		setA[x] = struct{}{}
	}
	setB := map[string]struct{}{}
	for _, x := range b {
		setB[x] = struct{}{}
	}
	inter := 0.0
	for token := range setA {
		if _, ok := setB[token]; ok {
			inter++
		}
	}
	return inter / (math.Sqrt(float64(len(setA))) * math.Sqrt(float64(len(setB))))
}

// codeExtensions lists the file types whose content is cut at declaration
// boundaries instead of fixed windows.
var codeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".rs": true,
	".java": true, ".c": true, ".cpp": true, ".h": true, ".rb": true, ".php": true,
}

// declarationPrefixes are the column-zero starts that open a top-level block in
// the supported languages. A line at column zero beginning with one of them is a
// natural chunk boundary.
var declarationPrefixes = []string{
	"func ", "type ", "var ", "const ",
	"def ", "async def ", "class ",
	"function ", "async function ", "export ", "interface ", "enum ", "let ",
	"fn ", "pub ", "impl ", "trait ", "struct ", "mod ", "use ", "static ",
	"module ", "namespace ", "template ", "public ", "private ", "protected ", "package ",
}

// isCodeFile reports whether the path is a source file we can split by symbols.
func isCodeFile(path string) bool {
	return codeExtensions[strings.ToLower(filepath.Ext(path))]
}

// isDeclarationLine reports whether a line opens a top-level block: it starts at
// column zero and matches a declaration prefix (or an ATX heading in Markdown).
func isDeclarationLine(line string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return false
	}
	if strings.HasPrefix(line, "# ") {
		return true
	}
	for _, prefix := range declarationPrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// splitCodeChunks cuts a source file at declaration boundaries so a chunk is a
// whole function, type or class instead of an arbitrary window. Segments larger
// than maxSize fall back to the fixed-size splitter, and non-code files use it
// directly.
func splitCodeChunks(text, path string, maxSize int) []Chunk {
	if maxSize <= 0 {
		maxSize = 800
	}
	if !isCodeFile(path) {
		return splitChunks(text, maxSize, 200)
	}
	lines := strings.Split(text, "\n")
	bounds := []int{1}
	for i, line := range lines {
		if i == 0 {
			continue
		}
		if isDeclarationLine(line) {
			bounds = append(bounds, i+1)
		}
	}
	if len(bounds) < 2 {
		return splitChunks(text, maxSize, 200)
	}
	bounds = append(bounds, len(lines)+1)

	out := make([]Chunk, 0, len(bounds))
	for i := 0; i < len(bounds)-1; i++ {
		startLine := bounds[i]
		endLine := bounds[i+1] - 1
		if endLine < startLine {
			continue
		}
		segment := strings.Join(lines[startLine-1:endLine], "\n")
		if len([]rune(segment)) > maxSize {
			for _, sub := range splitChunks(segment, maxSize, 200) {
				out = append(out, Chunk{Start: startLine + sub.Start - 1, End: startLine + sub.End - 1, Text: sub.Text})
			}
			continue
		}
		out = append(out, Chunk{Start: startLine, End: endLine, Text: segment})
	}
	return out
}
