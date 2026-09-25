package slash

import (
	"fmt"
	"strings"
)

func runJevstyleCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printJevstyleStatus(c)
		return nil
	}
	sub := strings.TrimSpace(args[0])
	switch strings.ToLower(sub) {
	case "status":
		printJevstyleStatus(c)
	case "off", "disable", "none":
		c.Cfg.JevstyleModel = ""
		fmt.Fprintln(c.Out, "JEV Style model disabled for this session.")
	default:
		if strings.EqualFold(sub, "set") && len(args) > 1 {
			sub = strings.TrimSpace(args[1])
		} else if len(args) > 1 {
			fmt.Fprintln(c.Out, "Invalid model name format.")
			return nil
		}
		if !modelNameRe.MatchString(sub) {
			fmt.Fprintln(c.Out, "Invalid model name format.")
			return nil
		}
		c.Cfg.JevstyleModel = sub
		fmt.Fprintf(c.Out, "JEV Style model set to: %s (run /save to persist)\n", c.Cfg.JevstyleModel)
	}
	return nil
}

func printJevstyleStatus(c *Ctx) {
	if strings.TrimSpace(c.Cfg.JevstyleModel) == "" {
		fmt.Fprintln(c.Out, "JEV Style: no model configured (JEVSTYLE_MODEL).")
	} else {
		fmt.Fprintf(c.Out, "JEV Style: on (%s)\n", strings.TrimSpace(c.Cfg.JevstyleModel))
	}
}
