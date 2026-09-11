// Command checker validates a code-edit e2e run: the project's tests must pass
// and only the allowed source files may have changed (no test, config, or
// unrelated edits, and no new files).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func run(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func main() {
	dir := flag.String("dir", "", "project directory")
	allow := flag.String("allow", "calc/calc.go,text/text.go", "comma-separated files allowed to change")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "FAIL: -dir is required")
		os.Exit(2)
	}
	allowed := map[string]bool{}
	for _, p := range strings.Split(*allow, ",") {
		if p = strings.TrimSpace(p); p != "" {
			allowed[p] = true
		}
	}

	// 1. The tests must actually pass.
	if out, err := run(*dir, "go", "test", "./..."); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: go test ./... failed:\n%s\n", out)
		os.Exit(1)
	}

	// 2. Only the allowed source files may have changed.
	out, err := run(*dir, "git", "status", "--porcelain")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: git status: %v\n%s\n", err, out)
		os.Exit(1)
	}
	changed := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, " -> ") {
			fmt.Fprintf(os.Stderr, "FAIL: unexpected rename: %s\n", line)
			os.Exit(1)
		}
		if len(line) < 4 {
			fmt.Fprintf(os.Stderr, "FAIL: unparseable git status line: %q\n", line)
			os.Exit(1)
		}
		path := strings.TrimSpace(line[3:])
		if !allowed[path] {
			fmt.Fprintf(os.Stderr, "FAIL: unexpected change to %q\n", path)
			os.Exit(1)
		}
		changed++
	}
	if changed == 0 {
		fmt.Fprintln(os.Stderr, "FAIL: no source files were changed, but tests needed fixing")
		os.Exit(1)
	}

	fmt.Printf("PASS: go test ./... passes; %d allowed file(s) changed\n", changed)
}
