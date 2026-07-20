package mcp

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type mcpConfigFile struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type envFlags []string

func (e *envFlags) String() string {
	return strings.Join(*e, ", ")
}

func (e *envFlags) Set(value string) error {
	*e = append(*e, value)
	return nil
}

// RunCLI handles the "mcp" subcommand execution.
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
	case "remove":
		return handleRemove(configDir, cwd, subArgs)
	default:
		return fmt.Errorf("unknown mcp subcommand: %q. Run 'vibe-coder mcp' for help", subcommand)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  vibe-coder mcp list")
	fmt.Println("        List configured MCP servers from both global and project configurations.")
	fmt.Println()
	fmt.Println("  vibe-coder mcp add [flags] <name> <command> [args...]")
	fmt.Println("        Add or update an MCP server configuration.")
	fmt.Println()
	fmt.Println("  vibe-coder mcp remove [flags] <name>")
	fmt.Println("        Remove an MCP server configuration.")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --global")
	fmt.Println("        Target the global configuration instead of project-local.")
	fmt.Println("  --env KEY=VALUE")
	fmt.Println("        Environment variable(s) (can be specified multiple times; only valid for 'add').")
}

func handleList(configDir, cwd string) error {
	globalPath := filepath.Join(configDir, "mcp.json")
	localPath := filepath.Join(cwd, ".vibe-coder", "mcp.json")

	fmt.Printf("Project-local config: %s\n", localPath)
	if err := printConfigFile(localPath); err != nil {
		return err
	}

	fmt.Println()

	fmt.Printf("Global config: %s\n", globalPath)
	if err := printConfigFile(globalPath); err != nil {
		return err
	}

	return nil
}

func printConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  (No configuration file found)")
			return nil
		}
		return fmt.Errorf("read file %s: %w", path, err)
	}

	var cfg mcpConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}

	if len(cfg.MCPServers) == 0 {
		fmt.Println("  No MCP servers configured.")
		return nil
	}

	// Sort server names for stable output
	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		srv := cfg.MCPServers[name]
		fmt.Printf("  Server: %s\n", name)
		fmt.Printf("    Command: %s %s\n", srv.Command, strings.Join(srv.Args, " "))
		if len(srv.Env) > 0 {
			fmt.Println("    Env:")
			keys := make([]string, 0, len(srv.Env))
			for k := range srv.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("      %s=%s\n", k, srv.Env[k])
			}
		}
	}
	return nil
}

func handleAdd(configDir, cwd string, args []string) error {
	fs := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	var isGlobal bool
	var envs envFlags
	fs.BoolVar(&isGlobal, "global", false, "target global configuration")
	fs.Var(&envs, "env", "environment variables in KEY=VALUE format (can be specified multiple times)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	parsedArgs := fs.Args()
	if len(parsedArgs) < 2 {
		return errors.New("missing <name> or <command>. Usage: vibe-coder mcp add [flags] <name> <command> [args...]")
	}

	name := parsedArgs[0]
	command := parsedArgs[1]
	serverArgs := parsedArgs[2:]

	targetPath := filepath.Join(cwd, ".vibe-coder", "mcp.json")
	if isGlobal {
		targetPath = filepath.Join(configDir, "mcp.json")
	}

	cfg, err := loadOrEmpty(targetPath)
	if err != nil {
		return err
	}

	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]ServerConfig)
	}

	envMap := make(map[string]string)
	for _, envStr := range envs {
		parts := strings.SplitN(envStr, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid environment variable format: %q. Must be KEY=VALUE", envStr)
		}
		envMap[parts[0]] = parts[1]
	}

	cfg.MCPServers[name] = ServerConfig{
		Command: command,
		Args:    serverArgs,
		Env:     envMap,
	}

	if err := saveConfig(targetPath, cfg); err != nil {
		return err
	}

	fmt.Printf("Added/updated server %q in: %s\n", name, targetPath)
	return nil
}

func handleRemove(configDir, cwd string, args []string) error {
	fs := flag.NewFlagSet("mcp remove", flag.ContinueOnError)
	var isGlobal bool
	fs.BoolVar(&isGlobal, "global", false, "target global configuration")

	if err := fs.Parse(args); err != nil {
		return err
	}

	parsedArgs := fs.Args()
	if len(parsedArgs) != 1 {
		return errors.New("missing server name. Usage: vibe-coder mcp remove [flags] <name>")
	}

	name := parsedArgs[0]

	targetPath := filepath.Join(cwd, ".vibe-coder", "mcp.json")
	if isGlobal {
		targetPath = filepath.Join(configDir, "mcp.json")
	}

	cfg, err := loadOrEmpty(targetPath)
	if err != nil {
		return err
	}

	if cfg.MCPServers == nil || cfg.MCPServers[name].Command == "" {
		return fmt.Errorf("server %q not found in config: %s", name, targetPath)
	}

	delete(cfg.MCPServers, name)

	if err := saveConfig(targetPath, cfg); err != nil {
		return err
	}

	fmt.Printf("Removed server %q from: %s\n", name, targetPath)
	return nil
}

func loadOrEmpty(path string) (mcpConfigFile, error) {
	var cfg mcpConfigFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config file: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config JSON: %w", err)
	}
	return cfg, nil
}

func saveConfig(path string, cfg mcpConfigFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config to JSON: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}
