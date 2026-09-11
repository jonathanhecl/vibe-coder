// Command checker validates a web-research e2e run. It requires a report.md
// that is long enough, cites several distinct real URLs (at least two of which
// are still reachable), contains a Sources section, and includes verifiable
// facts about the researched topic. It exits non-zero on any failure.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// factKeywords are markers of the Go programming language topic. The report
// must contain at least 3 so a generic/unfocused summary does not pass.
var factKeywords = []string{
	"google", "2009", "pike", "thompson", "griesemer",
	"open source", "gopher", "bell labs",
}

var urlRe = regexp.MustCompile(`https?://[^\s<>"']+`)

// trimURL drops trailing sentence punctuation and any unmatched ")".
func trimURL(raw string) string {
	u := strings.TrimRight(raw, ".,;:")
	for strings.HasSuffix(u, ")") && strings.Count(u, ")") > strings.Count(u, "(") {
		u = strings.TrimSuffix(u, ")")
	}
	return u
}

// normalizeForMatch lowercases and collapses separators so "open-source",
// "open_source" and "open  source" all match the "open source" keyword.
func normalizeForMatch(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func extractURLs(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range urlRe.FindAllString(text, -1) {
		u := trimURL(raw)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func urlReachable(client *http.Client, url string) bool {
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func main() {
	dir := flag.String("dir", ".", "directory containing report.md")
	flag.Parse()

	reportPath := filepath.Join(*dir, "report.md")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: read report: %v\n", err)
		os.Exit(1)
	}
	text := string(data)
	lower := strings.ToLower(text)

	problems := 0
	words := len(strings.Fields(text))
	if words < 150 {
		fmt.Fprintf(os.Stderr, "FAIL: report too short (%d words, want >= 150)\n", words)
		problems++
	}

	urls := extractURLs(text)
	if len(urls) < 3 {
		fmt.Fprintf(os.Stderr, "FAIL: report cites %d distinct URL(s), want >= 3\n", len(urls))
		problems++
	}
	if !strings.Contains(lower, "## sources") {
		fmt.Fprintf(os.Stderr, "FAIL: report is missing a '## Sources' section\n")
		problems++
	}

	normalized := normalizeForMatch(text)
	facts := 0
	for _, k := range factKeywords {
		if strings.Contains(normalized, k) {
			facts++
		}
	}
	if facts < 3 {
		fmt.Fprintf(os.Stderr, "FAIL: report mentions only %d topic fact marker(s), want >= 3\n", facts)
		problems++
	}

	// Best-effort reachability: at least two cited URLs must still resolve.
	client := &http.Client{Timeout: 20 * time.Second}
	reachable := 0
	for _, u := range urls {
		if urlReachable(client, u) {
			reachable++
			if reachable >= 2 {
				break
			}
		}
	}
	if reachable < 2 {
		fmt.Fprintf(os.Stderr, "FAIL: only %d cited URL(s) returned 200, want >= 2\n", reachable)
		problems++
	}

	if problems > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %d problem(s); words=%d urls=%d facts=%d reachable=%d\n",
			problems, words, len(urls), facts, reachable)
		os.Exit(1)
	}
	fmt.Printf("PASS: words=%d urls=%d facts=%d reachable=%d\n", words, len(urls), facts, reachable)
}
