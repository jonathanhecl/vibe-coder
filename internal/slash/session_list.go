package slash

import (
	"fmt"
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
	default:
		return runResume(c, args[0])
	}
}

func runSessionsList(c *Ctx) error {
	infos, err := session.ListSessions(c.Cfg)
	if err != nil {
		return err
	}
	st := tui.NewStyle(c.Out)
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
	header := fmt.Sprintf("  %-2s %-32s %-16s %-5s  %s", "", "ID", "MODIFIED", "MSGS", "PREVIEW")
	fmt.Fprintln(c.Out, st.Dim(header))
	for _, info := range infos {
		marker := "  "
		if info.IsCurrentProject {
			marker = st.BoldGreen("*")
		}
		preview := info.Preview
		if preview == "" {
			preview = st.Dim("(no user message)")
		}
		row := fmt.Sprintf("  %s %-32s %-16s %5d  %s",
			marker,
			st.Cyan(info.ID),
			st.Gray(info.ModTime.Local().Format("2006-01-02 15:04")),
			info.MessageCount,
			preview,
		)
		fmt.Fprintln(c.Out, row)
	}
	fmt.Fprintln(c.Out, st.Dim("(* = current project path | use /resume <id>, /session <id>, or /sessions delete <id>)"))
	return nil
}
