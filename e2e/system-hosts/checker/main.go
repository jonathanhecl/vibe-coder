// Command checker validates a system-hosts e2e run: the hosts file must map
// the requested address to the requested name exactly once (no duplicates, no
// wrong IP) and must still contain the entries that were there before.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func normalize(line string) string {
	return strings.Join(strings.Fields(line), " ")
}

func main() {
	hostsPath := flag.String("hosts", "", "path to the hosts file under test")
	ip := flag.String("ip", "192.168.0.33", "address the alias must map to")
	name := flag.String("name", "mac-mini.local", "alias that must be present")
	keep := flag.String("keep", "127.0.0.1 localhost|10.0.0.5 nas.local|192.168.0.10 printer.lan",
		"pipe-separated entries that must remain in the file")
	flag.Parse()

	if *hostsPath == "" {
		fmt.Fprintln(os.Stderr, "FAIL: -hosts is required")
		os.Exit(2)
	}
	data, err := os.ReadFile(*hostsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: read hosts file: %v\n", err)
		os.Exit(1)
	}

	problems := 0
	matches := 0
	lines := strings.Split(string(data), "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		normalized = append(normalized, normalize(line))
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		lineIP := fields[0]
		for _, host := range fields[1:] {
			if strings.EqualFold(host, *name) {
				if lineIP == *ip {
					matches++
				} else {
					fmt.Fprintf(os.Stderr, "FAIL: %s is mapped to %s, want %s\n", *name, lineIP, *ip)
					problems++
				}
			}
		}
	}

	if matches == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: no entry maps %s to %s\n", *ip, *name)
		problems++
	} else if matches > 1 {
		fmt.Fprintf(os.Stderr, "FAIL: %d entries map %s to %s, want exactly 1\n", matches, *ip, *name)
		problems++
	}

	for _, entry := range strings.Split(*keep, "|") {
		entry = normalize(entry)
		if entry == "" {
			continue
		}
		found := false
		for _, line := range normalized {
			if line == entry {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "FAIL: pre-existing entry %q is missing\n", entry)
			problems++
		}
	}

	if problems > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %d problem(s)\n", problems)
		os.Exit(1)
	}
	fmt.Printf("PASS: %s maps to %s exactly once; %d pre-existing entry(ies) preserved\n",
		*ip, *name, len(strings.Split(*keep, "|")))
}
