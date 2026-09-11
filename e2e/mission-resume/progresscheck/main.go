// Command progresscheck reports whether a results.jsonl file holds at least
// N complete, parseable JSON records. The resume e2e uses it to hard-kill the
// agent only at a clean record boundary, never mid-write, so the crash is
// deterministic and the checker stays strict.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	path := flag.String("file", "", "results file to inspect")
	min := flag.Int("min", 1, "minimum number of complete records")
	flag.Parse()

	if *path == "" {
		os.Exit(2)
	}
	file, err := os.Open(*path)
	if err != nil {
		fmt.Println(0)
		os.Exit(1)
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			fmt.Println(count)
			os.Exit(1)
		}
		if _, ok := record["id"]; !ok {
			fmt.Println(count)
			os.Exit(1)
		}
		count++
	}
	fmt.Println(count)
	if scanner.Err() != nil || count < *min {
		os.Exit(1)
	}
	os.Exit(0)
}
