package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type AutoTest struct {
	cwd string
}

func NewAutoTest(cwd string) *AutoTest {
	return &AutoTest{cwd: cwd}
}

func (a *AutoTest) Enabled() bool {
	_, cmd := a.detect()
	return len(cmd) > 0
}

func (a *AutoTest) RunAfterEdit(ctx context.Context, filePath string) string {
	name, _ := a.detect()
	cmdArgs := buildAutoTestCommand(name, filePath)
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
	if !shouldRunAutoTestForFile(testName, filePath) {
		return nil
	}
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	switch testName {
	case "pytest":
		return []string{"pytest", "-x", "--no-header", cleanPath}
	case "vitest":
		return []string{"npm", "run", "test", "--silent", "--", "--run", cleanPath}
	case "cargo":
		return []string{"cargo", "test", "--quiet"}
	case "go":
		dir := filepath.Dir(cleanPath)
		if dir == "." || dir == "" {
			dir = "./"
		}
		return []string{"go", "test", "./" + filepath.ToSlash(strings.TrimPrefix(dir, "./"))}
	default:
		return nil
	}
}
