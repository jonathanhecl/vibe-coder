package slash

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
)

// contextsStore returns the shared pinned-context store, creating a local
// one when the dispatcher was built without it (e.g. in tests).
func contextsStore(c *Ctx) *contextfiles.Store {
	if c.Contexts != nil {
		return c.Contexts
	}
	c.Contexts = contextfiles.NewStore()
	return c.Contexts
}

// syncContextPins keeps the session sidecar in sync with the store so the
// pinned list survives /save + --resume. Failures are reported but never
// break the command itself.
func syncContextPins(c *Ctx) {
	if c.Session == nil {
		return
	}
	c.Session.SetPinnedContexts(contextsStore(c).Paths())
	if err := c.Session.Save(); err != nil {
		fmt.Fprintf(c.Out, "warning: failed to persist pinned contexts: %v\n", err)
	}
}

// restorePinnedContexts reloads session-pinned files into the shared store.
// It is called after every session Load so --resume keeps the guides alive.
// Missing files are reported, never fatal.
func restorePinnedContexts(c *Ctx) {
	if c == nil || c.Session == nil {
		return
	}
	paths := c.Session.PinnedContexts()
	if len(paths) == 0 {
		return
	}
	store := contextsStore(c)
	loaded := 0
	for _, p := range paths {
		if _, _, err := store.Add(p); err != nil {
			fmt.Fprintf(c.Out, "warning: pinned context %q could not be reloaded: %v\n", p, err)
			continue
		}
		loaded++
	}
	if loaded > 0 {
		fmt.Fprintf(c.Out, "Restored %d pinned context file(s).\n", loaded)
	}
}

// splitContextPaths splits a raw argument tail into paths, honouring
// double quotes so files with spaces keep working.
func splitContextPaths(raw string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, r := range raw {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == ' ' || r == '\t':
			if inQuotes {
				cur.WriteRune(r)
			} else {
				flush()
			}
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func printContextUsage(c *Ctx) {
	fmt.Fprintln(c.Out, "Usage:")
	fmt.Fprintln(c.Out, "  /context <file.md|file.txt>   Pin a guide file (asks to add/replace when one is already pinned)")
	fmt.Fprintln(c.Out, "  /context add <file...>       Accumulate another guide file")
	fmt.Fprintln(c.Out, "  /context replace <file...>   Drop all pinned files and pin these instead")
	fmt.Fprintln(c.Out, "  /context list                Show pinned files")
	fmt.Fprintln(c.Out, "  /context drop <name|#|path>  Unpin one file")
	fmt.Fprintln(c.Out, "  /context clear               Unpin all files")
}

func printPinnedList(c *Ctx) {
	entries := contextsStore(c).List()
	if len(entries) == 0 {
		fmt.Fprintln(c.Out, "No context files pinned. Use /context <file.md|file.txt>.")
		return
	}
	fmt.Fprintf(c.Out, "Pinned context files (%d):\n", len(entries))
	for i, e := range entries {
		rel := e.Path
		if c.Cfg != nil {
			if r, err := filepath.Rel(c.Cfg.Cwd, e.Path); err == nil && !strings.HasPrefix(r, "..") {
				rel = r
			}
		}
		fmt.Fprintf(c.Out, "  %d. %s (%s, %d chars)\n", i+1, e.Name, rel, len(e.Content))
	}
}

// runContextCommand implements /context. The trimmed full line is passed so
// paths containing spaces survive parsing.
func runContextCommand(c *Ctx, line string) error {
	store := contextsStore(c)
	rest := strings.TrimSpace(strings.TrimPrefix(line, "/context"))

	if rest == "" || rest == "list" || rest == "ls" {
		printPinnedList(c)
		return nil
	}

	lower := strings.ToLower(rest)
	switch {
	case lower == "clear":
		store.Clear()
		syncContextPins(c)
		fmt.Fprintln(c.Out, "Cleared all pinned context files.")
		return nil
	case lower == "help" || lower == "-h" || lower == "--help":
		printContextUsage(c)
		return nil
	}

	fields := strings.Fields(rest)
	verb := strings.ToLower(fields[0])
	switch verb {
	case "add", "replace":
		raw := strings.TrimSpace(rest[len(fields[0]):])
		paths := splitContextPaths(raw)
		if len(paths) == 0 {
			fmt.Fprintf(c.Out, "Usage: /context %s <file.md|file.txt> [...]\n", verb)
			return nil
		}
		if verb == "replace" {
			entries, err := store.Replace(paths)
			if err != nil {
				return err
			}
			syncContextPins(c)
			fmt.Fprintf(c.Out, "Replaced pinned context with %d file(s).\n", len(entries))
			printPinnedList(c)
			return nil
		}
		for _, p := range paths {
			entry, isNew, err := store.Add(p)
			if err != nil {
				return err
			}
			if isNew {
				fmt.Fprintf(c.Out, "Pinned context: %s\n", entry.Name)
			} else {
				fmt.Fprintf(c.Out, "Refreshed context: %s\n", entry.Name)
			}
		}
		syncContextPins(c)
		return nil
	case "drop", "remove", "rm", "del", "delete":
		target := strings.TrimSpace(rest[len(fields[0]):])
		target = strings.Trim(target, `"`)
		if target == "" {
			fmt.Fprintln(c.Out, "Usage: /context drop <name|#|path>")
			return nil
		}
		removed, err := store.Remove(target)
		if err != nil {
			return err
		}
		syncContextPins(c)
		fmt.Fprintf(c.Out, "Dropped context: %s\n", removed.Name)
		return nil
	case "show", "cat":
		target := strings.Trim(strings.TrimSpace(rest[len(fields[0]):]), `"`)
		entries := store.List()
		if target == "" {
			if len(entries) == 0 {
				printPinnedList(c)
				return nil
			}
			fmt.Fprintln(c.Out, trimForDisplay(entries[0].Content, 2000))
			return nil
		}
		for _, e := range entries {
			if e.Name == target || e.Path == target {
				fmt.Fprintln(c.Out, trimForDisplay(e.Content, 2000))
				return nil
			}
		}
		return fmt.Errorf("pinned context %q not found", target)
	}

	// Bare form: /context <path>. When nothing is pinned yet, pin it
	// directly. Otherwise ask the user to choose explicitly instead of
	// guessing between accumulating and replacing.
	paths := splitContextPaths(rest)
	if len(paths) == 0 {
		printContextUsage(c)
		return nil
	}
	if len(paths) > 1 {
		for _, p := range paths {
			if _, _, err := store.Add(p); err != nil {
				return err
			}
		}
		syncContextPins(c)
		fmt.Fprintf(c.Out, "Pinned %d context file(s).\n", len(paths))
		return nil
	}
	if store.Has() {
		return askAppendOrReplace(c, store, paths[0])
	}
	if _, err := os.Lstat(strings.Trim(paths[0], `"`)); err != nil {
		printContextUsage(c)
		return nil
	}
	entry, _, err := store.Add(paths[0])
	if err != nil {
		return err
	}
	syncContextPins(c)
	fmt.Fprintf(c.Out, "Pinned context: %s\n", entry.Name)
	return nil
}

// askAppendOrReplace handles a bare /context <path> when files are already
// pinned. With an interactive prompter it asks Append vs Replace in
// English; without one it prints the non-interactive guidance.
func askAppendOrReplace(c *Ctx, store *contextfiles.Store, rawPath string) error {
	// Validate before asking so a bad path surfaces its error instead of
	// prompting first.
	preview, err := contextfiles.LoadEntry(rawPath)
	if err != nil {
		return err
	}
	if c.Prompter == nil {
		fmt.Fprintf(c.Out, "You already have %d pinned context file(s):\n", store.Count())
		printPinnedList(c)
		fmt.Fprintf(c.Out, "Use `/context add %s` to append or `/context replace %s` to replace.\n", rawPath, rawPath)
		return nil
	}
	fmt.Fprintf(c.Out, "You already have %d pinned context file(s):\n", store.Count())
	printPinnedList(c)
	answer, err := c.Prompter.GetInput(fmt.Sprintf("Pin %q as well? [A]ppend / [R]eplace / [C]ancel: ", preview.Name))
	if err != nil {
		fmt.Fprintln(c.Out, "Cancelled. Kept existing pinned contexts.")
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "a", "append":
		entry, isNew, err := store.Add(rawPath)
		if err != nil {
			return err
		}
		syncContextPins(c)
		if isNew {
			fmt.Fprintf(c.Out, "Appended context: %s\n", entry.Name)
		} else {
			fmt.Fprintf(c.Out, "Refreshed context: %s\n", entry.Name)
		}
		return nil
	case "r", "replace":
		entries, err := store.Replace([]string{rawPath})
		if err != nil {
			return err
		}
		syncContextPins(c)
		fmt.Fprintf(c.Out, "Replaced pinned context with %d file(s).\n", len(entries))
		printPinnedList(c)
		return nil
	case "c", "cancel", "n", "no", "q", "quit":
		fmt.Fprintln(c.Out, "Cancelled. Kept existing pinned contexts.")
		return nil
	default:
		fmt.Fprintf(c.Out, "Unknown choice %q. Kept existing pinned contexts. Use /context add ... to append or /context replace ... to replace.\n", strings.TrimSpace(answer))
		return nil
	}
}
