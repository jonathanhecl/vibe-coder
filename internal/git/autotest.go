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
	name, cmdArgs := a.detect()
	if len(cmdArgs) == 0 {
		return ""
	}
	if !shouldRunAutoTestForFile(name, filePath) {
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
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filePath)))
	if ext == "" {
		return true
	}
	switch testName {
	case "pytest":
		return ext == ".py"
	case "vitest":
		return ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".mjs" || ext == ".cjs"
	case "cargo":
		return ext == ".rs"
	case "go":
		return ext == ".go"
	default:
		return true
	}
}
