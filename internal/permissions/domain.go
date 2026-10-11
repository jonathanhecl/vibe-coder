package permissions

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ToolTargetPath extracts the file or directory target path from tool parameters.
// Returns "" if the tool does not target a file or directory path.
func ToolTargetPath(toolName string, params map[string]any) string {
	if params == nil {
		return ""
	}
	tool := strings.ToLower(strings.TrimSpace(toolName))
	switch tool {
	case "read", "write", "edit", "describeimage":
		for _, key := range []string{"file_path", "image_path", "path"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				return strings.TrimSpace(p)
			}
		}
	case "notebookedit", "notebookread":
		for _, key := range []string{"notebook_path", "file_path", "path"} {
			if p, ok := params[key].(string); ok && strings.TrimSpace(p) != "" {
				return strings.TrimSpace(p)
			}
		}
	case "glob", "grep":
		if p, ok := params["path"].(string); ok && strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
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
	ws := m.WorkspaceDir()
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
		// Target does not exist yet (e.g. Write to a new file). Check nearest existing directory ancestor.
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
