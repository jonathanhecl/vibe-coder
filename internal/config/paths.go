package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func resolveDirs(goos, homeDir, localAppData string) (string, string, error) {
	if goos == "windows" {
		if localAppData == "" {
			return "", "", errors.New("LOCALAPPDATA is empty")
		}
		base := filepath.Join(localAppData, "vibe-coder")
		return base, base, nil
	}
	if homeDir == "" {
		return "", "", errors.New("home directory is empty")
	}
	configDir := filepath.Join(homeDir, ".config", "vibe-coder")
	stateDir := filepath.Join(homeDir, ".local", "state", "vibe-coder")
	return configDir, stateDir, nil
}

func envFirstNonEmpty(keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}
