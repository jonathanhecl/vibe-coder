package git

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type AutoTest struct {
	cwd string

	mu           sync.Mutex
	detectedName string
	detected     bool
}

func NewAutoTest(cwd string) *AutoTest {
	return &AutoTest{cwd: cwd}
}

func (a *AutoTest) Enabled() bool {
	_, cmd := a.detectCached()
	return len(cmd) > 0
}

func (a *AutoTest) RunAfterEdit(ctx context.Context, filePath string) string {
	name, _ := a.detectCached()
	cmdArgs := a.buildAutoTestCommand(name, filePath)
	if len(cmdArgs) == 0 {
		return ""
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = a.cwd
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	output := string(out)
	if len(output) > 2048 {
		output = output[len(output)-2048:]
	}
	return fmt.Sprintf("[AUTO-TEST] %s failed\n%s", name, strings.TrimSpace(output))
}

// detectCached remembers the first positive detection. Runner markers
// (go.mod, Cargo.toml, …) rarely appear mid-session, and once one does the
// answer cannot change for a fixed cwd — while a negative answer is
// re-checked so enabling a runner mid-session still takes effect.
func (a *AutoTest) detectCached() (string, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.detected {
		return a.detectedName, suiteCommand(a.detectedName)
	}
	name, cmd := a.detect()
	if len(cmd) > 0 {
		a.detected = true
		a.detectedName = name
	}
	return name, cmd
}

// suiteCommand is the full-suite default, used for empty paths and as
// documentation of what each runner means. Targeted commands are built by
// buildAutoTestCommand.
func suiteCommand(name string) []string {
	switch name {
	case "pytest":
		return []string{"pytest", "-x", "--no-header"}
	case "vitest":
		return []string{"npm", "run", "test", "--silent", "--", "--run"}
	case "cargo":
		return []string{"cargo", "test", "--quiet"}
	case "go":
		return []string{"go", "test", "./..."}
	default:
		return nil
	}
}

func (a *AutoTest) detect() (string, []string) {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(a.cwd, name))
		return err == nil
	}
	if has("pyproject.toml") || has("pytest.ini") {
		return "pytest", []string{"pytest", "-x", "--no-header"}
	}
	if has("package.json") {
		data, err := os.ReadFile(filepath.Join(a.cwd, "package.json"))
		if err == nil && strings.Contains(string(data), `"vitest"`) {
			return "vitest", []string{"npm", "run", "test", "--silent", "--", "--run"}
		}
	}
	if has("Cargo.toml") {
		return "cargo", []string{"cargo", "test", "--quiet"}
	}
	if has("go.mod") {
		return "go", []string{"go", "test", "./..."}
	}
	return "", nil
}

func shouldRunAutoTestForFile(testName, filePath string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(filePath)))
	if base == "" {
		return true
	}
	isTestLike := strings.HasPrefix(base, "test_") || strings.Contains(base, ".test.") || strings.Contains(base, "_test.")

	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filePath)))
	if ext == "" {
		return true
	}
	switch testName {
	case "pytest":
		return ext == ".py" && isTestLike
	case "vitest":
		return (ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".mjs" || ext == ".cjs") && isTestLike
	case "cargo":
		return ext == ".rs" && isTestLike
	case "go":
		return ext == ".go" && isTestLike
	default:
		return true
	}
}

func buildAutoTestCommand(testName, filePath string) []string {
	return newAutoTestCommandBuilder("", testName, filePath)
}

// buildAutoTestCommand resolves the test command for an edited file:
// files that are themselves tests run directly, source files resolve to
// their related test files, and anything without a test target resolves
// to nil (skip silently). An empty path keeps the legacy full-suite
// default for backward compatibility.
func (a *AutoTest) buildAutoTestCommand(testName, filePath string) []string {
	cwd := ""
	if a != nil {
		cwd = a.cwd
	}
	return newAutoTestCommandBuilder(cwd, testName, filePath)
}

func newAutoTestCommandBuilder(cwd, testName, filePath string) []string {
	trimmed := strings.TrimSpace(filePath)
	if trimmed == "" {
		return suiteCommand(testName)
	}
	switch testName {
	case "pytest":
		return pytestCommand(cwd, trimmed)
	case "vitest":
		return vitestCommand(cwd, trimmed)
	case "cargo":
		return cargoCommand(cwd, trimmed)
	case "go":
		return goCommand(cwd, trimmed)
	default:
		return nil
	}
}

func pytestCommand(cwd, filePath string) []string {
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	if strings.ToLower(filepath.Ext(cleanPath)) != ".py" {
		return nil
	}
	if shouldRunAutoTestForFile("pytest", filePath) {
		return []string{"pytest", "-x", "--no-header", cleanPath}
	}
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	related := existingJoin(cwd, pytestRelated(relOf(cwd, cleanPath)))
	if len(related) == 0 {
		return nil
	}
	return append([]string{"pytest", "-x", "--no-header"}, related...)
}

func vitestCommand(cwd, filePath string) []string {
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	switch strings.ToLower(filepath.Ext(cleanPath)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
	default:
		return nil
	}
	if shouldRunAutoTestForFile("vitest", filePath) {
		return []string{"npm", "run", "test", "--silent", "--", "--run", cleanPath}
	}
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	related := existingJoin(cwd, vitestRelated(relOf(cwd, cleanPath)))
	if len(related) == 0 {
		return nil
	}
	return append([]string{"npm", "run", "test", "--silent", "--", "--run"}, related...)
}

func cargoCommand(cwd, filePath string) []string {
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	if strings.ToLower(filepath.Ext(cleanPath)) != ".rs" {
		return nil
	}
	rel := relOf(cwd, cleanPath)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 2 && parts[0] == "tests" {
		stem := strings.TrimSuffix(parts[1], ".rs")
		return []string{"cargo", "test", "--quiet", "--test", stem}
	}
	if shouldRunAutoTestForFile("cargo", filePath) {
		return []string{"cargo", "test", "--quiet"}
	}
	if len(parts) >= 2 && parts[0] == "src" {
		rest := parts[1:]
		rest[len(rest)-1] = strings.TrimSuffix(rest[len(rest)-1], ".rs")
		if last := rest[len(rest)-1]; last == "mod" {
			rest = rest[:len(rest)-1]
		}
		if len(rest) == 1 && (rest[0] == "main" || rest[0] == "lib") {
			return []string{"cargo", "test", "--quiet"}
		}
		if len(rest) > 0 {
			return []string{"cargo", "test", "--quiet", strings.Join(rest, "::")}
		}
		return []string{"cargo", "test", "--quiet"}
	}
	return nil
}

func goCommand(cwd, filePath string) []string {
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	if strings.ToLower(filepath.Ext(cleanPath)) != ".go" {
		return nil
	}
	if strings.TrimSpace(cwd) != "" {
		abs := cleanPath
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		if info, err := os.Lstat(abs); err != nil || info.IsDir() {
			return nil
		}
	}
	dir := filepath.Dir(cleanPath)
	if filepath.IsAbs(dir) && strings.TrimSpace(cwd) != "" {
		if rel, err := filepath.Rel(cwd, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			dir = rel
		}
	}
	var pkg string
	if filepath.IsAbs(dir) {
		pkg = dir
	} else {
		if dir == "." || dir == "" {
			dir = "./"
		}
		pkg = "./" + filepath.ToSlash(strings.TrimPrefix(dir, "./"))
	}
	if shouldRunAutoTestForFile("go", filePath) {
		if names := parseGoTestFuncs(resolveForRead(cwd, cleanPath)); len(names) > 0 {
			return []string{"go", "test", "-run", "^(" + strings.Join(names, "|") + ")$", pkg}
		}
	}
	return []string{"go", "test", pkg}
}

// relOf returns filePath relative to cwd when it is an absolute path under
// it; otherwise the cleaned path unchanged (tests and edge cases).
func relOf(cwd, filePath string) string {
	if strings.TrimSpace(cwd) != "" && filepath.IsAbs(filePath) {
		if rel, err := filepath.Rel(cwd, filePath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return filePath
}

// resolveForRead maps a possibly cwd-relative path to an absolute one for
// reading file contents.
func resolveForRead(cwd, filePath string) string {
	if filepath.IsAbs(filePath) || strings.TrimSpace(cwd) == "" {
		return filePath
	}
	return filepath.Join(cwd, filePath)
}

// existingJoin keeps the candidates (cwd-relative) that exist, returning
// absolute paths for the runner.
func existingJoin(cwd string, candidates []string) []string {
	out := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, c := range candidates {
		abs := c
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, c)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

// pytestRelated lists conventional test files for a source module, without
// touching disk: same directory plus tests/ mirrors.
func pytestRelated(rel string) []string {
	stem := strings.TrimSuffix(filepath.Base(rel), ".py")
	dir := filepath.Dir(rel)
	candidates := []string{
		filepath.Join(dir, "test_"+stem+".py"),
		filepath.Join(dir, stem+"_test.py"),
		filepath.Join("tests", "test_"+stem+".py"),
		filepath.Join("tests", stem+"_test.py"),
	}
	if dir != "." && dir != "" && dir != "tests" && !strings.HasPrefix(dir, "tests"+string(filepath.Separator)) {
		candidates = append(candidates,
			filepath.Join("tests", dir, "test_"+stem+".py"),
			filepath.Join("tests", dir, stem+"_test.py"),
		)
	}
	return candidates
}

var vitestExts = []string{".ts", ".tsx", ".js", ".jsx"}

// vitestRelated lists colocated and __tests__/ test files for a source
// module, trying the claimant extension plus the common TS/JS ones.
func vitestRelated(rel string) []string {
	dir := filepath.Dir(rel)
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	exts := []string{filepath.Ext(rel)}
	for _, e := range vitestExts {
		if e != exts[0] {
			exts = append(exts, e)
		}
	}
	var candidates []string
	for _, e := range exts {
		candidates = append(candidates,
			filepath.Join(dir, stem+".test"+e),
			filepath.Join(dir, stem+".spec"+e),
			filepath.Join(dir, "__tests__", stem+".test"+e),
			filepath.Join(dir, "__tests__", stem+".spec"+e),
		)
	}
	return candidates
}

var goTestFuncRe = regexp.MustCompile(`^\s*func\s+(Test[A-Za-z0-9_]+)\s*\(`)

const maxGoTestParseBytes = 1 << 20

// parseGoTestFuncs extracts top-level Test function names for -run
// filtering. Failures (missing file, oversize, no matches) return nil and
// the caller falls back to the whole package.
func parseGoTestFuncs(path string) []string {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxGoTestParseBytes {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var names []string
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		if m := goTestFuncRe.FindStringSubmatch(scanner.Text()); m != nil {
			if _, ok := seen[m[1]]; !ok {
				seen[m[1]] = struct{}{}
				names = append(names, m[1])
			}
		}
	}
	return names
}
