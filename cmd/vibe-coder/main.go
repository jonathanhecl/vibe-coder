package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/version"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	binName := filepath.Base(os.Args[0])
	if cfg.ShowHelp {
		fmt.Fprint(os.Stdout, config.Usage(binName))
		return
	}

	if cfg.ShowVer {
		fmt.Fprintf(os.Stdout, "vibe-coder %s\n", version.Value)
		return
	}

	if cfg.ListSessions {
		fmt.Fprintln(os.Stdout, "No hay sesiones todavía (MVP en progreso).")
		return
	}

	fmt.Fprintln(os.Stdout, "Inicialización MVP lista: wiring de agente pendiente en próximos hitos.")
}

