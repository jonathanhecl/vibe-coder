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
