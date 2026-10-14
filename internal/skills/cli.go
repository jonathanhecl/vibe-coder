package skills

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// RunCLI handles execution of the "skill" and "skills" subcommands.
func RunCLI(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "help", "-h", "--help":
		printUsage()
		return nil
	case "list":
		return handleList(cfg)
	case "add":
		return handleAdd(cfg.ConfigDir, cfg.Cwd, subArgs)
	default:
		return fmt.Errorf("unknown skill subcommand: %q. Run 'vibe skill' for help", subcommand)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  vibe skill list")
	fmt.Println("        List all discoverable skills, their source, path, and description.")
	fmt.Println()
	fmt.Println("  vibe skill add [flags] <name> <source_file_path>")
	fmt.Println("        Copy an existing Markdown skill file into vibe's skill directories.")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --global")
	fmt.Println("        Target the global skills directory instead of project-local.")
}

func handleList(cfg *config.Config) error {
	items := Load(cfg)
	if len(items) == 0 {
		fmt.Println("No skills found. Add skills under .vibe-coder/skills/ or <configDir>/skills/, or point SKILLS_DIRS at existing skill folders (e.g. ~/.config/opencode/skills).")
		return nil
	}

	fmt.Printf("Loaded skills (%d total):\n", len(items))
	for _, item := range items {
		fmt.Printf("  %s [%s]\n", item.Name, item.Source)
		fmt.Printf("    Path: %s\n", item.Path)
		if item.Description != "" {
			fmt.Printf("    %s\n", item.Description)
		}
	}
	return nil
}

func handleAdd(configDir, cwd string, args []string) error {
	fs := flag.NewFlagSet("skill add", flag.ContinueOnError)
	var isGlobal bool
	fs.BoolVar(&isGlobal, "global", false, "target global skills directory")

	if err := fs.Parse(args); err != nil {
		return err
	}

	parsedArgs := fs.Args()
	if len(parsedArgs) < 2 {
		return errors.New("missing <name> or <source_file_path>. Usage: vibe skill add [flags] <name> <source_file_path>")
	}

	name := parsedArgs[0]
	sourcePath := parsedArgs[1]

	// Basic validation of skill name (should be alphanumeric/dashes/underscores)
	if strings.ContainsAny(name, `/\:*?"<>|`) {
		return fmt.Errorf("invalid skill name %q: must not contain path delimiters or special characters", name)
	}

	// Read source file content
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read source skill file: %w", err)
	}

	targetDir := filepath.Join(cwd, ".vibe-coder", "skills")
	if isGlobal {
		targetDir = filepath.Join(configDir, "skills")
	}

	targetPath := filepath.Join(targetDir, name+".md")

	// Ensure destination directory exists
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}

	// Write skill file
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}

	fmt.Printf("Added skill %q in: %s\n", name, targetPath)
	return nil
}
