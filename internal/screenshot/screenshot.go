// Package screenshot captures a monitor or the active window in memory for
// the Screenshot tool. Captures are handed to the vision routing as pixels
// and are never persisted to the session or the transcript.
//
// No persistent files: backends that can write PNG to stdout (grim,
// ImageMagick import) are preferred. Backends that only write files
// (spectacle, gnome-screenshot, screencapture) write to a 0600 file inside a
// fresh 0700 directory from os.MkdirTemp under $XDG_RUNTIME_DIR (tmpfs, so
// it stays in RAM) falling back to os.TempDir(); the directory is removed
// right after the bytes are read.
package screenshot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "image/png" // register the PNG decoder for capture backends
)

// Target selects what to capture.
type Target string

const (
	// TargetScreen captures a monitor.
	TargetScreen Target = "screen"
	// TargetWindow captures the currently active window.
	TargetWindow Target = "window"
)

// Request describes a capture.
type Request struct {
	Target  Target // default screen
	Monitor string // screen only: "" or "all" = every monitor,
	// "current" = the monitor under the cursor/focused,
	// "1".."N" = 1-based monitor index
}

// captureTimeout bounds a single capture command so a hung compositor or
// permission prompt cannot stall the agent.
const captureTimeout = 15 * time.Second

// lookPath is exec.LookPath, replaceable in tests.
var lookPath = exec.LookPath

// runCommand executes a capture command and returns its combined stdout.
// Replaceable in tests.
var runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

// Normalize validates the request and fills defaults.
func (r Request) Normalize() (Request, error) {
	out := r
	out.Target = Target(strings.ToLower(strings.TrimSpace(string(out.Target))))
	out.Monitor = strings.ToLower(strings.TrimSpace(out.Monitor))
	if out.Target == "" {
		out.Target = TargetScreen
	}
	switch out.Target {
	case TargetScreen:
		switch out.Monitor {
		case "", "all":
			out.Monitor = "all"
		case "current":
		default:
			n, err := strconv.Atoi(out.Monitor)
			if err != nil || n < 1 {
				return Request{}, fmt.Errorf("invalid monitor %q: use \"all\", \"current\", or a 1-based monitor number", r.Monitor)
			}
		}
	case TargetWindow:
		if out.Monitor != "" {
			return Request{}, fmt.Errorf("the monitor option only applies to target \"screen\"")
		}
	default:
		return Request{}, fmt.Errorf("invalid target %q: use \"screen\" or \"window\"", r.Target)
	}
	return out, nil
}

// Label returns a human-readable description of what will be captured.
func (r Request) Label() string {
	if r.Target == TargetWindow {
		return "active window"
	}
	switch r.Monitor {
	case "current":
		return "current monitor"
	case "", "all":
		return "all monitors"
	default:
		return fmt.Sprintf("monitor %s", r.Monitor)
	}
}

// Capture grabs the requested pixels with a bounded timeout.
func Capture(ctx context.Context, req Request) (image.Image, error) {
	norm, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()
	return capture(callCtx, norm)
}

// decodePNG decodes PNG bytes from a capture backend.
func decodePNG(data []byte) (image.Image, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("capture produced no image data")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode captured image: %w", err)
	}
	return img, nil
}

// tempImageDir returns a fresh 0700 directory for file-only backends,
// preferably on tmpfs ($XDG_RUNTIME_DIR) so pixels never reach disk.
func tempImageDir() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return os.MkdirTemp(base, "vibe-shot-")
}

// captureToFile runs a file-only backend: it creates a temp dir, runs
// run(path) which must make the backend write path, reads the PNG, and
// removes the dir. The pixels live only in RAM afterwards.
func captureToFile(ctx context.Context, run func(path string) ([]byte, error)) (image.Image, error) {
	dir, err := tempImageDir()
	if err != nil {
		return nil, fmt.Errorf("create temp capture dir: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "shot.png")
	// Pre-create with 0600 so the pixels are private even if the backend
	// would default to looser permissions.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create temp capture file: %w", err)
	}
	f.Close()
	out, err := run(path)
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read captured image: %w", err)
	}
	return decodePNG(data)
}
