package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// pathMemory remembers absolute file paths the agent has observed via tool
// outputs (Glob lines, the file_path of a successful Read/Write/Edit, Grep
// hits, etc.) so we can rescue later tool calls that arrive with a sloppy
// relative path or just a basename.
//
// This is the agent's safety net for the very common LLM mistake of writing
// `<invoke name="Read">{"file_path":"AGENTS.md"}</invoke>` after just having
// listed it via Glob — the model knows the file exists, but loses the full
// path between turns.
type pathMemory struct {
	mu     sync.Mutex
	cwd    string
	byName map[string]map[string]struct{} // basename -> set of abs paths
}

func newPathMemory(cwd string) *pathMemory {
	abs, err := filepath.Abs(cwd)
	if err != nil || abs == "" {
		abs = cwd
	}
	return &pathMemory{
		cwd:    abs,
		byName: make(map[string]map[string]struct{}),
	}
}

// add stores an absolute path under its basename. Non-absolute or
// non-existent paths are ignored to keep the index trustworthy.
func (m *pathMemory) add(p string) {
	if p == "" || !filepath.IsAbs(p) {
		return
	}
	if _, err := os.Stat(p); err != nil {
		return
	}
	base := filepath.Base(p)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.byName[base]
	if !ok {
		set = make(map[string]struct{}, 2)
		m.byName[base] = set
	}
	set[p] = struct{}{}
}

// RememberToolResult mines a tool's output for absolute paths to index, and
// also indexes the resolved file_path of the call itself (so a successful
// Read of /foo/bar.go is enough to later rescue "bar.go").
//
// We deliberately keep this conservative: only file_path-style params and
// lines whose entire trimmed content is an absolute path that exists on
// disk are added. This avoids polluting the index with prose.
func (m *pathMemory) RememberToolResult(toolName string, params map[string]any, output string, isError bool) {
	if isError {
		return
	}
	if fp, ok := params["file_path"].(string); ok && fp != "" {
		m.add(fp)
	}
	for _, line := range strings.Split(output, "\n") {
		token := strings.TrimSpace(line)
		if token == "" {
			continue
		}
		// Grep emits "path:line:match"; take the first colon-separated
		// chunk that looks like an absolute path.
		if !filepath.IsAbs(token) {
			if i := strings.Index(token, ":"); i > 0 {
				head := token[:i]
				// On Windows "C:\foo" begins with a drive letter so we
				// must split on the *second* colon for that case.
				if len(head) == 1 && i+1 < len(token) {
					if j := strings.Index(token[i+1:], ":"); j > 0 {
						head = token[:i+1+j]
					}
				}
				if filepath.IsAbs(head) {
					token = head
				}
			}
		}
		m.add(token)
	}
}

// godotResToFilesystem maps Godot's res:// paths to real files under cwd
// (the project root). It also fixes mistaken Windows concatenations such as
// `D:\Proj\res://Main/foo.gd` by locating `res://` and taking the suffix as
// project-relative. Returns ("", false) when p does not contain `res://`.
func godotResToFilesystem(cwd, p string) (string, bool) {
	idx := strings.Index(strings.ToLower(p), "res://")
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimLeft(p[idx+len("res://"):], `/\`)
	if rest == "" {
		return "", false
	}
	rest = filepath.FromSlash(strings.ReplaceAll(rest, `\`, `/`))
	joined := filepath.Join(cwd, rest)
	absPath, err := filepath.Abs(joined)
	if err != nil {
		return "", false
	}
	rootAbs, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return absPath, true
}

// Resolve maps a (possibly relative or basename-only) path to an absolute
// path that exists, using strategies in order:
//  0. Godot res:// → absolute under cwd (always; marks rescued).
//  1. If already absolute and reachable, return as-is.
//  2. Join with the agent's cwd; use that if it exists.
//  3. Look up the basename in memory and accept it only if exactly one
//     candidate is registered (no ambiguous rescue). This also runs when (1)
//     or (2) fail — e.g. the model uses a wrong absolute path like
//     D:\Repo\file.go after Read succeeded with D:\deep\Repo\file.go.
//
// rescued is true when strategy 0 or 3 was needed, so the caller can surface a
// hint in the UI.
func (m *pathMemory) Resolve(p string) (abs string, rescued bool, ok bool) {
	if strings.TrimSpace(p) == "" {
		return "", false, false
	}
	p = strings.TrimSpace(p)
	if abs, ok := godotResToFilesystem(m.cwd, p); ok {
		return abs, true, true
	}
	if filepath.IsAbs(p) {
		if _, err := os.Stat(p); err == nil {
			return p, false, true
		}
	} else if joined := filepath.Join(m.cwd, p); joined != "" {
		if _, err := os.Stat(joined); err == nil {
			return joined, false, true
		}
	}
	if c, ok := m.rescueUniqueBasename(p); ok {
		return c, true, true
	}
	if filepath.IsAbs(p) {
		return p, false, false
	}
	return "", false, false
}

// rescueUniqueBasename returns the only indexed absolute path for filepath.Base(p).
func (m *pathMemory) rescueUniqueBasename(p string) (string, bool) {
	base := filepath.Base(p)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	candidates := m.byName[base]
	if len(candidates) != 1 {
		return "", false
	}
	for c := range candidates {
		return c, true
	}
	return "", false
}

// Candidates returns every absolute path indexed under the basename of p,
// sorted lexicographically for stable disambiguation prompts. Returns nil
// when nothing matches. Callers use this together with a sidecar
// disambiguator when Resolve declines because of ambiguity.
func (m *pathMemory) Candidates(p string) []string {
	if strings.TrimSpace(p) == "" {
		return nil
	}
	base := filepath.Base(p)
	m.mu.Lock()
	set := m.byName[base]
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	m.mu.Unlock()
	if len(out) == 0 {
		return nil
	}
	// Stable order so cache keys for DisambiguatePath stay deterministic.
	sortStrings(out)
	return out
}

// sortStrings is a tiny helper kept package-local so we don't drag in
// "sort" everywhere; insertion sort is fine for the small slices we get
// from path candidates (typically 2-5 entries).
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// pathParamKeyForTool returns the parameter name that holds a file path for
// the given tool, or "" if the tool doesn't take a path we should rescue.
//
// Bash and Glob deliberately return "" — Bash takes a free-form command and
// Glob takes a directory which is allowed to be relative.
func pathParamKeyForTool(toolName string) string {
	switch toolName {
	case "Read", "Write", "Edit", "NotebookEdit":
		return "file_path"
	}
	return ""
}
