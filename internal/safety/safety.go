package safety

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	backgroundPatterns = []*regexp.Regexp{
		regexp.MustCompile(`&\s*$`),
		regexp.MustCompile(`&\s*\)`),
		regexp.MustCompile(`&\s*;`),
		regexp.MustCompile(`\bnohup\b`),
		regexp.MustCompile(`\bsetsid\b`),
		regexp.MustCompile(`\bdisown\b`),
		regexp.MustCompile(`\bscreen\s+-[dDm]`),
		regexp.MustCompile(`\btmux\b.*\b(new|send)`),
		regexp.MustCompile(`\bat\s+now\b`),
		regexp.MustCompile(`bash\s+-c\s+['"][^'"]*&`),
		regexp.MustCompile(`sh\s+-c\s+['"][^'"]*&`),
	}

	dangerousPatterns = []struct {
		pattern *regexp.Regexp
		reason  string
	}{
		{regexp.MustCompile(`(?i)\bcurl\b.*\|\s*\bsh\b`), "curl piped to shell"},
		{regexp.MustCompile(`(?i)\bwget\b.*\|\s*\bsh\b`), "wget piped to shell"},
		{regexp.MustCompile(`(?i)\brm\s+-rf\s+/`), "rm -rf from root"},
		{regexp.MustCompile(`(?i)\bmkfs\b`), "format filesystem"},
		{regexp.MustCompile(`(?i)\bdd\b.*\bof=/dev/`), "dd to device"},
		{regexp.MustCompile(`(?i)>\s*/etc/`), "overwrite system files"},
		{regexp.MustCompile(`(?i)\beval\b.*\bbase64\b`), "eval with base64 decode"},
	}
)

var protectedWriteIndicators = []string{
	">", ">>", "tee ", "mv ", "cp ", "echo ", "cat ", "sed ", "dd ", "install ",
	"printf ", "perl ", "python", "ruby ", "bash -c", "sh -c", "ln ",
}

func CleanEnv() []string {
	allow := map[string]struct{}{
		"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {}, "SHELL": {}, "TERM": {}, "LANG": {},
		"TMPDIR": {}, "TMP": {}, "TEMP": {}, "DISPLAY": {}, "WAYLAND_DISPLAY": {}, "XDG_RUNTIME_DIR": {},
		"XDG_DATA_HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {}, "SSH_AUTH_SOCK": {}, "EDITOR": {},
		"VISUAL": {}, "PAGER": {}, "HOSTNAME": {}, "PWD": {}, "OLDPWD": {}, "SHLVL": {}, "COLORTERM": {},
		"TERM_PROGRAM": {}, "COLUMNS": {}, "LINES": {}, "NO_COLOR": {}, "FORCE_COLOR": {}, "CC": {}, "CXX": {},
		"CFLAGS": {}, "LDFLAGS": {}, "PKG_CONFIG_PATH": {}, "GOPATH": {}, "GOROOT": {}, "CARGO_HOME": {},
		"RUSTUP_HOME": {}, "JAVA_HOME": {}, "NVM_DIR": {}, "PYENV_ROOT": {}, "VIRTUAL_ENV": {}, "CONDA_DEFAULT_ENV": {},
		"OLLAMA_HOST": {}, "PYTHONPATH": {}, "NODE_PATH": {}, "GEM_HOME": {}, "RBENV_ROOT": {},
	}
	sensitivePrefixes := []string{
		"CLAUDECODE", "CLAUDE_CODE", "ANTHROPIC", "OPENAI", "AWS_SECRET", "AWS_SESSION", "GITHUB_TOKEN",
		"GH_TOKEN", "GITLAB_", "HF_TOKEN", "AZURE_",
	}
	sensitiveSubstrings := []string{
		"_SECRET", "_TOKEN", "_KEY", "_PASSWORD", "_CREDENTIAL", "_API_KEY", "DATABASE_URL",
		"REDIS_URL", "MONGO_URI", "PRIVATE_KEY", "_AUTH", "KUBECONFIG",
	}

	env := os.Environ()
	out := make([]string, 0, len(env))
	hasPath := false
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(key)
		if upper == "PATH" {
			hasPath = true
		}
		if _, ok := allow[upper]; ok || strings.HasPrefix(upper, "LC_") {
			out = append(out, key+"="+value)
			continue
		}
		block := false
		for _, prefix := range sensitivePrefixes {
			if strings.HasPrefix(upper, prefix) {
				block = true
				break
			}
		}
		if !block {
			for _, sub := range sensitiveSubstrings {
				if strings.Contains(upper, sub) {
					block = true
					break
				}
			}
		}
		if !block {
			out = append(out, key+"="+value)
		}
	}
	if !hasPath {
		out = append(out, "PATH="+os.Getenv("PATH"))
	}
	return out
}

func IsBackgroundCommand(cmd string) bool {
	for _, pattern := range backgroundPatterns {
		if pattern.MatchString(cmd) {
			return true
		}
	}
	return false
}

func IsDangerousCommand(cmd string) (bool, string) {
	for _, pattern := range dangerousPatterns {
		if pattern.pattern.MatchString(cmd) {
			return true, pattern.reason
		}
	}
	return false, ""
}

func IsProtectedWrite(cmd string) bool {
	lower := strings.ToLower(cmd)
	if !(strings.Contains(lower, "permissions.json") || strings.Contains(lower, ".vibe-coder.json") || strings.Contains(lower, "config.json")) {
		return false
	}
	for _, indicator := range protectedWriteIndicators {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}

func IsProtectedPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	expanded := path
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			expanded = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	resolved, err := filepath.EvalSymlinks(expanded)
	if err == nil && resolved != "" {
		expanded = resolved
	}
	clean := filepath.Clean(expanded)
	lower := strings.ToLower(clean)

	if strings.HasPrefix(clean, "/proc/") || strings.HasPrefix(clean, "/sys/") || strings.HasPrefix(clean, "/dev/") {
		return true
	}
	if clean == "/etc/shadow" || clean == "/etc/sudoers" {
		return true
	}

	if strings.Contains(lower, ".ssh"+string(filepath.Separator)+"id_") {
		return true
	}
	if strings.Contains(lower, ".aws"+string(filepath.Separator)+"credentials") {
		return true
	}
	if strings.Contains(lower, ".config"+string(filepath.Separator)+"gcloud"+string(filepath.Separator)) {
		return true
	}
	if strings.Contains(lower, ".kube"+string(filepath.Separator)+"config") {
		return true
	}

	if runtime.GOOS == "windows" {
		if strings.Contains(lower, `c:\windows\system32\config\`) {
			return true
		}
		appData := strings.ToLower(os.Getenv("APPDATA"))
		if appData != "" && strings.HasPrefix(lower, filepath.Clean(appData+`\Microsoft\Crypto\`)) {
			return true
		}
	}
	return false
}
