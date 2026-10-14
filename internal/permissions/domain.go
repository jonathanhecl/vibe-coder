package permissions

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ToolTargetPath extracts the primary file or directory target path from tool parameters.
// Returns "" if the tool does not target a file or directory path.
func ToolTargetPath(toolName string, params map[string]any) string {
	paths := ToolTargetPaths(toolName, params, "")
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

// ToolTargetPaths extracts all candidate file or directory target paths from tool parameters.
// If ws is non-empty, relative candidate paths are checked against the ws domain.
func ToolTargetPaths(toolName string, params map[string]any, ws string) []string {
	if params == nil {
		return nil
	}
	tool := strings.ToLower(strings.TrimSpace(toolName))
	switch tool {
	case "read", "readmany", "write", "edit", "describeimage":
		var out []string
		for _, key := range []string{"paths", "file_paths", "files", "filepaths"} {
			switch v := params[key].(type) {
			case []any:
				for _, x := range v {
					if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
						out = append(out, strings.TrimSpace(s))
					}
				}
			case []string:
				for _, s := range v {
					if strings.TrimSpace(s) != "" {
						out = append(out, strings.TrimSpace(s))
					}
				}
			case string:
				if strings.TrimSpace(v) != "" {
					out = append(out, strings.TrimSpace(v))
				}
			}
		}
		if len(out) > 0 {
			return deduplicatePaths(out)
		}
		for _, key := range []string{"file_path", "filePath", "image_path", "path", "target", "filename", "file"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				return []string{strings.TrimSpace(p)}
			}
		}
		return nil
	case "notebookedit", "notebookread":
		for _, key := range []string{"notebook_path", "file_path", "filePath", "path"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				return []string{strings.TrimSpace(p)}
			}
		}
	case "gitdiff":
		for _, key := range []string{"file_path", "filePath", "path", "target"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				return []string{strings.TrimSpace(p)}
			}
		}
	case "glob":
		for _, key := range []string{"path", "dir", "directory", "file_path", "filePath", "root"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				return []string{strings.TrimSpace(p)}
			}
		}
		if pat, ok := params["pattern"].(string); ok && strings.TrimSpace(pat) != "" {
			pat = strings.TrimSpace(pat)
			if hasPathIndicators(pat) {
				dir := extractPatternDir(pat)
				if dir != "" && dir != "." {
					return []string{dir}
				}
			}
		}
	case "grep":
		for _, key := range []string{"path", "dir", "directory", "file_path", "filePath", "root"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				return []string{strings.TrimSpace(p)}
			}
		}
		if g, ok := params["glob"].(string); ok && strings.TrimSpace(g) != "" {
			g = strings.TrimSpace(g)
			if hasPathIndicators(g) {
				dir := extractPatternDir(g)
				if dir != "" && dir != "." {
					return []string{dir}
				}
			}
		}
	case "bash", "interactivebash":
		var candidates []string
		for _, key := range []string{"workdir", "cwd", "dir"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				wd := strings.TrimSpace(p)
				if ws == "" || !IsPathInsideDomain(ws, wd) {
					candidates = append(candidates, wd)
				}
			}
		}
		if cmd, ok := params["command"].(string); ok && strings.TrimSpace(cmd) != "" {
			cmdCandidates := ExtractCommandTargetPaths(cmd, ws)
			candidates = append(candidates, cmdCandidates...)
		}
		return deduplicatePaths(candidates)
	}
	return nil
}

// ExtractCommandTargetPaths scans a shell command line for arguments, targets, or redirections
// that point to filesystem paths outside ws (if ws is non-empty) or candidate paths in general.
func ExtractCommandTargetPaths(command string, ws string) []string {
	subcmds := tokenizeCommandSubcommands(command)
	var out []string
	seen := map[string]struct{}{}
	for _, words := range subcmds {
		candidates := extractCandidatePathsFromSubcommand(words)
		for _, c := range candidates {
			clean := cleanCandidatePath(c, ws)
			if clean == "" {
				continue
			}
			if ws != "" && IsPathInsideDomain(ws, clean) {
				continue
			}
			if _, ok := seen[clean]; !ok {
				seen[clean] = struct{}{}
				out = append(out, clean)
			}
		}
	}
	return out
}

func tokenizeCommandSubcommands(command string) [][]string {
	var subcmds [][]string
	var curWords []string
	var curWord strings.Builder

	inSingle := false
	inDouble := false
	escaped := false

	flushWord := func() {
		if curWord.Len() > 0 {
			curWords = append(curWords, curWord.String())
			curWord.Reset()
		}
	}

	flushSubcmd := func() {
		flushWord()
		if len(curWords) > 0 {
			subcmds = append(subcmds, curWords)
			curWords = nil
		}
	}

	chars := []rune(command)
	for i := 0; i < len(chars); i++ {
		ch := chars[i]

		if escaped {
			curWord.WriteRune(ch)
			escaped = false
			continue
		}

		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}

		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				curWord.WriteRune(ch)
			}
			continue
		}

		if inDouble {
			if ch == '"' {
				inDouble = false
			} else {
				curWord.WriteRune(ch)
			}
			continue
		}

		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case ';', '\n':
			flushSubcmd()
		case '&':
			if i+1 < len(chars) && chars[i+1] == '&' {
				i++
				flushSubcmd()
			} else {
				flushSubcmd()
			}
		case '|':
			if i+1 < len(chars) && chars[i+1] == '|' {
				i++
				flushSubcmd()
			} else {
				flushSubcmd()
			}
		case ' ', '\t', '\r':
			flushWord()
		default:
			curWord.WriteRune(ch)
		}
	}
	flushSubcmd()
	return subcmds
}

func extractCandidatePathsFromSubcommand(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	var candidates []string
	isCD := words[0] == "cd"

	for i, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if w == ">" || w == ">>" || w == "<" || w == "2>" || w == "2>>" || w == "&>" {
			continue
		}
		for _, op := range []string{"2>>", "2>", ">>", ">", "<", "&>"} {
			if strings.HasPrefix(w, op) {
				w = strings.TrimPrefix(w, op)
				break
			}
		}
		w = strings.Trim(w, `"'`)
		if w == "" {
			continue
		}

		if isCD && i > 0 && !strings.HasPrefix(w, "-") {
			candidates = append(candidates, w)
			continue
		}

		if i == 0 {
			if isSystemBinaryPath(w) {
				continue
			}
			if hasPathIndicators(w) {
				candidates = append(candidates, w)
			}
			continue
		}

		target := w
		if strings.HasPrefix(w, "-") {
			if eq := strings.Index(w, "="); eq > 0 && eq < len(w)-1 {
				target = strings.Trim(w[eq+1:], `"'`)
			} else if (strings.HasPrefix(w, "-I/") || strings.HasPrefix(w, "-L/")) && len(w) > 2 {
				target = w[2:]
			} else {
				continue
			}
		}

		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
			strings.HasPrefix(target, "ftp://") || strings.HasPrefix(target, "git@") ||
			strings.HasPrefix(target, "ssh://") {
			continue
		}

		if isVirtualDevice(target) {
			continue
		}

		if hasPathIndicators(target) {
			candidates = append(candidates, target)
		}
	}
	return candidates
}

func isSystemBinaryPath(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(p))
	lower := strings.ToLower(clean)
	systemBinPrefixes := []string{
		"/bin/", "/usr/bin/", "/usr/local/bin/", "/sbin/", "/usr/sbin/",
		"/opt/homebrew/bin/", "/snap/bin/",
	}
	for _, pre := range systemBinPrefixes {
		if strings.HasPrefix(lower, pre) {
			return true
		}
	}
	if runtime.GOOS == "windows" {
		winPrefixes := []string{
			`c:/windows/system32/`, `c:/windows/`, `c:/program files/`,
		}
		for _, pre := range winPrefixes {
			if strings.HasPrefix(lower, pre) {
				return true
			}
		}
	}
	return false
}

func isVirtualDevice(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(p))
	switch clean {
	case "/dev/null", "/dev/zero", "/dev/stdin", "/dev/stdout", "/dev/stderr", "nul", "NUL":
		return true
	}
	return false
}

func hasPathIndicators(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return true
	}
	if strings.HasPrefix(p, "~") {
		return true
	}
	if strings.HasPrefix(p, "$HOME/") || strings.HasPrefix(p, "${HOME}/") {
		return true
	}
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return true
	}
	cleanSlash := filepath.ToSlash(p)
	parts := strings.Split(cleanSlash, "/")
	for _, part := range parts {
		if part == ".." {
			return true
		}
	}
	return false
}

func cleanCandidatePath(c, ws string) string {
	c = strings.TrimSpace(c)
	if c == "" {
		return ""
	}
	if strings.HasPrefix(c, "$HOME/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c = filepath.Join(home, strings.TrimPrefix(c, "$HOME/"))
		}
	} else if strings.HasPrefix(c, "${HOME}/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c = filepath.Join(home, strings.TrimPrefix(c, "${HOME}/"))
		}
	} else if strings.HasPrefix(c, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(c, "~"), string(filepath.Separator)))
		}
	}
	if filepath.IsAbs(c) {
		return filepath.Clean(c)
	}
	if ws != "" {
		return filepath.Clean(filepath.Join(ws, c))
	}
	return filepath.Clean(c)
}

func extractPatternDir(pat string) string {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return ""
	}
	firstWild := strings.IndexAny(pat, "*?[]")
	if firstWild >= 0 {
		prefix := pat[:firstWild]
		lastSlash := strings.LastIndexAny(prefix, "/\\")
		if lastSlash >= 0 {
			dir := prefix[:lastSlash]
			if dir == "" && (strings.HasPrefix(pat, "/") || strings.HasPrefix(pat, "\\")) {
				return "/"
			}
			return dir
		}
		if strings.HasPrefix(pat, "/") || strings.HasPrefix(pat, "\\") {
			return "/"
		}
		return "."
	}
	return filepath.Dir(pat)
}

func deduplicatePaths(paths []string) []string {
	if len(paths) <= 1 {
		return paths
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// WorkspaceDir returns the current workspace directory domain.
func (m *Manager) WorkspaceDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workspaceDir
}

// SetWorkspaceDir overrides the active workspace directory domain.
func (m *Manager) SetWorkspaceDir(dir string) {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean != "" {
		if abs, err := filepath.Abs(clean); err == nil {
			clean = filepath.Clean(abs)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workspaceDir = clean
}

func (m *Manager) canonicalAbsPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "$HOME/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			p = filepath.Join(home, strings.TrimPrefix(p, "$HOME/"))
		}
	} else if strings.HasPrefix(p, "${HOME}/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			p = filepath.Join(home, strings.TrimPrefix(p, "${HOME}/"))
		}
	} else if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(filepath.Separator)))
		}
	}
	ws := m.WorkspaceDir()
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) && ws != "" {
		clean = filepath.Clean(filepath.Join(ws, clean))
	}
	return clean
}

// IsInsideDomain reports whether path is within the workspace directory domain
// (the folder where vibe was executed and any subdirectories beneath it).
func (m *Manager) IsInsideDomain(path string) bool {
	return IsPathInsideDomain(m.WorkspaceDir(), path)
}

// IsPathInsideDomain reports whether path is within the ws directory domain.
func IsPathInsideDomain(ws, path string) bool {
	if ws == "" {
		return true
	}
	cleanWS := filepath.Clean(ws)
	if !filepath.IsAbs(cleanWS) {
		if abs, err := filepath.Abs(cleanWS); err == nil {
			cleanWS = filepath.Clean(abs)
		}
	}

	target := strings.TrimSpace(path)
	if target == "" {
		return true
	}
	if strings.HasPrefix(target, "$HOME/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			target = filepath.Join(home, strings.TrimPrefix(target, "$HOME/"))
		}
	} else if strings.HasPrefix(target, "${HOME}/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			target = filepath.Join(home, strings.TrimPrefix(target, "${HOME}/"))
		}
	} else if strings.HasPrefix(target, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			target = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(target, "~"), string(filepath.Separator)))
		}
	}
	cleanTarget := filepath.Clean(target)
	if !filepath.IsAbs(cleanTarget) {
		cleanTarget = filepath.Clean(filepath.Join(cleanWS, cleanTarget))
	}

	// Lexical check
	checkWS := cleanWS
	checkTarget := cleanTarget
	if runtime.GOOS == "windows" {
		checkWS = strings.ToLower(cleanWS)
		checkTarget = strings.ToLower(cleanTarget)
	}
	rel, err := filepath.Rel(checkWS, checkTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	// Symlink check: ensure neither the target nor its parent resolves outside cleanWS
	evalWS, errWS := filepath.EvalSymlinks(cleanWS)
	if errWS != nil {
		evalWS = cleanWS
	}
	evalTarget, errTarget := filepath.EvalSymlinks(cleanTarget)
	if errTarget == nil {
		checkEvalWS := evalWS
		checkEvalTarget := evalTarget
		if runtime.GOOS == "windows" {
			checkEvalWS = strings.ToLower(evalWS)
			checkEvalTarget = strings.ToLower(evalTarget)
		}
		relEval, err := filepath.Rel(checkEvalWS, checkEvalTarget)
		if err != nil || relEval == ".." || strings.HasPrefix(relEval, ".."+string(filepath.Separator)) {
			return false
		}
	} else {
		ancestor := cleanTarget
		for {
			parent := filepath.Dir(ancestor)
			if parent == ancestor || parent == "." {
				break
			}
			ancestor = parent
			if info, err := os.Stat(ancestor); err == nil && info.IsDir() {
				if evalAncestor, err := filepath.EvalSymlinks(ancestor); err == nil {
					checkEvalWS := evalWS
					checkEvalAncestor := evalAncestor
					if runtime.GOOS == "windows" {
						checkEvalWS = strings.ToLower(evalWS)
						checkEvalAncestor = strings.ToLower(evalAncestor)
					}
					relEval, err := filepath.Rel(checkEvalWS, checkEvalAncestor)
					if err != nil || relEval == ".." || strings.HasPrefix(relEval, ".."+string(filepath.Separator)) {
						return false
					}
				}
				break
			}
		}
	}

	return true
}

// UnapprovedOutDomainPaths returns any paths targeted by the tool that are
// outside the workspace domain and have not yet been approved (by the user
// in this session, persistently, or via user-provided clipboard paths).
func (m *Manager) UnapprovedOutDomainPaths(toolName string, params map[string]any) []string {
	allPaths := ToolTargetPaths(toolName, params, m.WorkspaceDir())
	if len(allPaths) == 0 {
		return nil
	}
	var unapproved []string
	for _, p := range allPaths {
		if m.IsInsideDomain(p) {
			continue
		}
		if m.IsUserProvidedPath(p) {
			continue
		}
		if m.IsOutDomainAllowed(p) {
			continue
		}
		unapproved = append(unapproved, p)
	}
	return deduplicatePaths(unapproved)
}

// IsUserProvidedPath reports whether path matches a file explicitly supplied
// by the user (such as an image pasted via /paste).
func (m *Manager) IsUserProvidedPath(path string) bool {
	p := strings.TrimSpace(path)
	if p == "" {
		return false
	}
	if abs, err := filepath.Abs(p); err == nil && abs != "" {
		p = filepath.Clean(abs)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userProvided == nil {
		return false
	}
	if _, ok := m.userProvided[p]; ok {
		return true
	}
	base := filepath.Base(p)
	if full, ok := m.userProvided[base]; ok && full == p {
		return true
	}
	return false
}

// isPathUnder reports whether target is equal to or located beneath parent.
func isPathUnder(parent, target string) bool {
	p := filepath.Clean(parent)
	t := filepath.Clean(target)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
		t = strings.ToLower(t)
	}
	if p == t {
		return true
	}
	rel, err := filepath.Rel(p, t)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isSystemOrHomeDir(dir string) bool {
	clean := filepath.Clean(dir)
	slashed := filepath.ToSlash(clean)
	lower := strings.ToLower(slashed)

	if clean == "/" || clean == "\\" || filepath.VolumeName(clean) == clean || clean == filepath.VolumeName(clean)+"\\" || clean == filepath.VolumeName(clean)+"/" {
		return true
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cleanHome := filepath.Clean(home)
		if strings.EqualFold(clean, cleanHome) {
			return true
		}
		if strings.EqualFold(clean, filepath.Dir(cleanHome)) {
			return true
		}
	}

	systemPrefixes := []string{
		"/etc", "/bin", "/sbin", "/usr", "/var", "/tmp", "/root", "/sys", "/proc", "/dev", "/opt",
	}
	for _, sys := range systemPrefixes {
		if lower == sys || lower == sys+"/" {
			return true
		}
	}

	if runtime.GOOS == "windows" {
		winDirs := []string{
			`c:/windows`, `c:/program files`, `c:/program files (x86)`, `c:/programdata`,
		}
		for _, wd := range winDirs {
			if lower == wd || lower == wd+"/" {
				return true
			}
		}
	}
	return false
}

// IsOutDomainAllowed reports whether path was approved for out-of-domain access.
func (m *Manager) IsOutDomainAllowed(path string) bool {
	clean := m.canonicalAbsPath(path)
	if clean == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for allowed := range m.allowedOutDomain {
		if isPathUnder(allowed, clean) {
			return true
		}
	}
	for k, v := range m.persistent {
		if v == "allow" && strings.HasPrefix(k, "out_domain:") {
			allowed := strings.TrimPrefix(k, "out_domain:")
			if isPathUnder(allowed, clean) {
				return true
			}
		}
	}
	return false
}

// IsOutDomainBlocked reports whether path was denied out-of-domain access.
func (m *Manager) IsOutDomainBlocked(path string) bool {
	clean := m.canonicalAbsPath(path)
	if clean == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for denied := range m.deniedOutDomain {
		if isPathUnder(denied, clean) {
			return true
		}
	}
	for k, v := range m.persistent {
		if v == "deny" && strings.HasPrefix(k, "out_domain:") {
			denied := strings.TrimPrefix(k, "out_domain:")
			if isPathUnder(denied, clean) {
				return true
			}
		}
	}
	return false
}

// AllowOutDomainSession records in-memory permission for an out-of-domain path.
func (m *Manager) AllowOutDomainSession(targetPath string) {
	clean := m.canonicalAbsPath(targetPath)
	if clean == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.allowedOutDomain == nil {
		m.allowedOutDomain = map[string]struct{}{}
	}
	if m.deniedOutDomain != nil {
		delete(m.deniedOutDomain, clean)
	}
	m.allowedOutDomain[clean] = struct{}{}

	fi, err := os.Stat(clean)
	if (err == nil && !fi.IsDir()) || err != nil {
		parent := filepath.Dir(clean)
		if !isSystemOrHomeDir(parent) {
			m.allowedOutDomain[parent] = struct{}{}
			if m.deniedOutDomain != nil {
				delete(m.deniedOutDomain, parent)
			}
		}
	}
}

// DenyOutDomainSession records in-memory block for an out-of-domain path.
func (m *Manager) DenyOutDomainSession(targetPath string) {
	clean := m.canonicalAbsPath(targetPath)
	if clean == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deniedOutDomain == nil {
		m.deniedOutDomain = map[string]struct{}{}
	}
	if m.allowedOutDomain != nil {
		delete(m.allowedOutDomain, clean)
	}
	m.deniedOutDomain[clean] = struct{}{}
}

// AllowOutDomainPersistent records persistent permission for an out-of-domain path.
func (m *Manager) AllowOutDomainPersistent(targetPath string) {
	m.AllowOutDomainSession(targetPath)
	clean := m.canonicalAbsPath(targetPath)
	if clean == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.persistent, "out_domain:"+clean)
	m.persistent["out_domain:"+clean] = "allow"
	_ = m.savePersistentLocked()
}

// DenyOutDomainPersistent records persistent denial for an out-of-domain path.
func (m *Manager) DenyOutDomainPersistent(targetPath string) {
	m.DenyOutDomainSession(targetPath)
	clean := m.canonicalAbsPath(targetPath)
	if clean == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.persistent, "out_domain:"+clean)
	m.persistent["out_domain:"+clean] = "deny"
	_ = m.savePersistentLocked()
}
