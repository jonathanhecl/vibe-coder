package slash

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runSessionsCommand(c *Ctx, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "", "list", "ls":
		return runSessionsList(c)
	case "delete", "del", "rm", "remove":
		return runSessionsDelete(c, args[1:])
	default:
		fmt.Fprintln(c.Out, "Usage: /sessions [list | delete <id> | delete --all]")
		return nil
	}
}

func runSessionAlias(c *Ctx, args []string) error {
	if len(args) == 0 {
		return runSessionsList(c)
	}
	first := strings.ToLower(strings.TrimSpace(args[0]))
	switch first {
	case "list", "ls", "delete", "del", "rm", "remove":
		return runSessionsCommand(c, args)
	case "last", "latest":
		return runResumeLast(c)
	default:
		return loadSessionByID(c, args[0])
	}
}

func runSessionsList(c *Ctx) error {
	infos, err := session.ListSessions(c.Cfg)
	if err != nil {
		return err
	}
	st := tui.NewStyle(c.Out)
	if c.Cfg != nil && session.HasIsolatedSession(c.Cfg.Cwd) {
		fmt.Fprintf(c.Out, "%s %s\n\n",
			st.Magenta("Isolated session active in"),
			st.Dim(c.Cfg.Cwd),
		)
	}
	if len(infos) == 0 {
		fmt.Fprintf(c.Out, "%s %s\n",
			st.Yellow("No sessions found in"),
			st.Dim(c.Cfg.SessionsDir),
		)
		return nil
	}
	fmt.Fprintf(c.Out, "%s %s\n",
		st.BoldCyan("Sessions in"),
		st.Dim(c.Cfg.SessionsDir),
	)
	header := fmt.Sprintf("  %-2s %-32s %-28s %-16s %-5s  %s", "", "ID", "PATH", "MODIFIED", "MSGS", "PREVIEW")
	fmt.Fprintln(c.Out, st.Dim(header))
	for _, info := range infos {
		marker := "  "
		pathColor := st.Gray
		if info.IsCurrentProject {
			marker = st.BoldGreen("*") + " "
			pathColor = st.Green
		}
		preview := info.Preview
		if preview == "" {
			preview = st.Dim("(no user message)")
		}
		shortPath := shortenProjectPath(info.ProjectPath, 28)
		row := fmt.Sprintf("  %s %-32s %s %-16s %5d  %s",
			marker,
			st.Cyan(info.ID),
			pathColor(fmt.Sprintf("%-28s", shortPath)),
			st.Gray(info.ModTime.Local().Format("2006-01-02 15:04")),
			info.MessageCount,
			preview,
		)
		fmt.Fprintln(c.Out, row)
	}
	fmt.Fprintln(c.Out, st.Dim("(* = current project path | use /resume, /session <id>, or /sessions delete <id>)"))
	return nil
}

// shortenProjectPath formats a project directory path for compact tabular
// display in session listings. It replaces the user's home directory with ~
// when possible and shortens long paths while preserving the base directory
// hierarchy (e.g. ".../Github/vibe-coder"). Empty paths return "-".
func shortenProjectPath(p string, maxLen int) string {
	clean := strings.TrimSpace(p)
	if clean == "" {
		return "-"
	}
	clean = filepath.Clean(clean)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if clean == home {
			clean = "~"
		} else if strings.HasPrefix(clean, home+string(filepath.Separator)) {
			clean = "~" + clean[len(home):]
		}
	}
	if len(clean) <= maxLen {
		return clean
	}
	sep := string(filepath.Separator)
	parts := strings.Split(clean, sep)
	if len(parts) <= 1 {
		if len(clean) > maxLen {
			return "..." + clean[len(clean)-(maxLen-3):]
		}
		return clean
	}
	tail := parts[len(parts)-1]
	prefix := "..." + sep
	if len(prefix+tail) > maxLen {
		avail := maxLen - len(prefix)
		if avail <= 0 {
			return prefix
		}
		return prefix + tail[len(tail)-avail:]
	}
	accum := tail
	for i := len(parts) - 2; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		candidate := part + sep + accum
		if len(prefix+candidate) <= maxLen {
			accum = candidate
		} else {
			break
		}
	}
	return prefix + accum
}
