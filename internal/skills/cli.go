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
func RunCLI(configDir, cwd string, args []string) error {
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
		return handleList(configDir, cwd)
	case "add":
		return handleAdd(configDir, cwd, subArgs)
	default:
		return fmt.Errorf("unknown skill subcommand: %q. Run 'vibe-coder skill' for help", subcommand)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  vibe-coder skill list")
	fmt.Println("        List all currently loaded skills, their paths, and previews.")
	fmt.Println()
	fmt.Println("  vibe-coder skill add [flags] <name> <source_file_path>")
	fmt.Println("        Copy an existing Markdown skill file into vibe-coder's skill directories.")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --global")
	fmt.Println("        Target the global skills directory instead of project-local.")
}

func handleList(configDir, cwd string) error {
	// Reuse loader's Load implementation by building a dummy config.
	cfg := &config.Config{
		ConfigDir: configDir,
		Cwd:       cwd,
	}

	items := Load(cfg)
	if len(items) == 0 {
		fmt.Println("No skills loaded.")
		return nil
	}

	fmt.Printf("Loaded skills (%d total):\n", len(items))
	for _, item := range items {
		fmt.Printf("  Skill: %s\n", item.Name)
		fmt.Printf("    Path: %s\n", item.Path)

		// Create a short preview of the content
		preview := strings.TrimSpace(item.Content)
		if lines := strings.SplitN(preview, "\n", 2); len(lines) > 0 {
			preview = lines[0]
			if len(preview) > 80 {
				preview = preview[:77] + "..."
			}
		}
		if preview == "" {
			preview = "(empty content)"
		}
		fmt.Printf("    Preview: %s\n", preview)
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
		return errors.New("missing <name> or <source_file_path>. Usage: vibe-coder skill add [flags] <name> <source_file_path>")
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
