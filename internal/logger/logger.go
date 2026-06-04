package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

var (
	mu         sync.Mutex
	fileLogger *log.Logger
	logFile    *os.File
)

// Init opens vibe-coder.log under configDir, truncating it to store logs
// from the last execution only.
func Init(configDir string) (io.Closer, error) {
	mu.Lock()
	defer mu.Unlock()

	// If already initialized, close previous log file
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
		fileLogger = nil
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir for logging: %w", err)
	}

	path := filepath.Join(configDir, "vibe-coder.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	logFile = f
	fileLogger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	fileLogger.Println("[INFO] Logger initialized")

	return f, nil
}

// Infof writes an info log entry to the log file.
func Infof(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if fileLogger != nil {
		fileLogger.Printf("[INFO] "+format, args...)
	}
}

// Errorf writes an error log entry to the log file.
func Errorf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if fileLogger != nil {
		fileLogger.Printf("[ERROR] "+format, args...)
	}
}

// Debugf writes a debug log entry to the log file.
func Debugf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if fileLogger != nil {
		fileLogger.Printf("[DEBUG] "+format, args...)
	}
}
