// Command probe exercises the real WebSearch and WebFetch tools against the
// live internet, without a model. Useful to check connectivity and parsing
// before running the full web-research e2e.
//
//	go run ./e2e/web-research/probe "your search query"
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func main() {
	query := "Go programming language history"
	if len(os.Args) > 1 {
		query = strings.Join(os.Args[1:], " ")
	}
	ctx := context.Background()

	search := tools.NewWebSearchTool().Execute(ctx, map[string]any{"query": query})
	fmt.Printf("== WebSearch(%q) is_error=%t ==\n%s\n\n", query, search.IsError, search.Output)

	url := "https://go.dev/doc/"
	if len(os.Args) > 2 {
		url = os.Args[2]
	}
	fetch := tools.NewWebFetchTool().Execute(ctx, map[string]any{"url": url})
	fmt.Printf("== WebFetch(%q) is_error=%t ==\n", url, fetch.IsError)
	if fetch.IsError {
		fmt.Println(fetch.Output)
		return
	}
	preview := fetch.Output
	if len(preview) > 500 {
		preview = preview[:500]
	}
	fmt.Println(preview)
}
