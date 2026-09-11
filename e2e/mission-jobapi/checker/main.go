// Command checker validates an end-to-end mission run: every dataset item must
// have exactly one verified line in results.jsonl whose value matches the
// expected value. It exits non-zero on any mismatch.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

type datasetItem struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Expect string `json:"expect"`
}

type resultLine struct {
	ID       string `json:"id"`
	JobID    string `json:"job_id"`
	Value    string `json:"value"`
	Attempts int    `json:"attempts"`
	Status   string `json:"status"`
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func main() {
	dir := flag.String("dir", ".", "directory containing dataset.jsonl and results.jsonl")
	flag.Parse()

	datasetPath := filepath.Join(*dir, "dataset.jsonl")
	resultsPath := filepath.Join(*dir, "results.jsonl")

	datasetLines, err := readLines(datasetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: read dataset: %v\n", err)
		os.Exit(1)
	}
	resultsLines, err := readLines(resultsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: read results: %v\n", err)
		os.Exit(1)
	}

	order := make([]string, 0, len(datasetLines))
	expect := map[string]string{}
	for _, line := range datasetLines {
		var item datasetItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: bad dataset line: %v\n", err)
			os.Exit(1)
		}
		order = append(order, item.ID)
		expect[item.ID] = item.Expect
	}

	verified := map[string]resultLine{}
	verifiedOrder := make([]string, 0, len(resultsLines))
	problems := 0
	retries := 0
	for _, line := range resultsLines {
		var res resultLine
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: bad results line: %v\n", err)
			problems++
			continue
		}
		if res.Status != "verified" {
			continue
		}
		if _, ok := expect[res.ID]; !ok {
			fmt.Fprintf(os.Stderr, "FAIL: results contain unknown id %q\n", res.ID)
			problems++
			continue
		}
		if _, dup := verified[res.ID]; dup {
			fmt.Fprintf(os.Stderr, "FAIL: duplicate verified line for %q\n", res.ID)
			problems++
		}
		verified[res.ID] = res
		verifiedOrder = append(verifiedOrder, res.ID)
		if res.Attempts > 1 {
			retries++
		}
	}

	for _, id := range order {
		res, ok := verified[id]
		if !ok {
			fmt.Fprintf(os.Stderr, "FAIL: missing verified result for %q\n", id)
			problems++
			continue
		}
		if res.Value != expect[id] {
			fmt.Fprintf(os.Stderr, "FAIL: %q value=%q want %q\n", id, res.Value, expect[id])
			problems++
		}
	}

	// The guide requires results to be appended in dataset order; a resume
	// must not reorder or interleave the durable file.
	if len(verifiedOrder) == len(order) {
		for i, id := range order {
			if verifiedOrder[i] != id {
				fmt.Fprintf(os.Stderr, "FAIL: results out of order at line %d: got %q want %q\n", i+1, verifiedOrder[i], id)
				problems++
				break
			}
		}
	}

	if problems > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %d problem(s); %d/%d verified\n", problems, len(verified), len(order))
		os.Exit(1)
	}
	fmt.Printf("PASS: %d/%d items verified, %d used retries\n", len(verified), len(order), retries)
}
