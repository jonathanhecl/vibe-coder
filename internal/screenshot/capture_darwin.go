//go:build darwin

package screenshot

import (
	"context"
	"fmt"
	"image"
	"regexp"
	"strconv"
	"strings"
)

// capture drives `screencapture`, which writes only to files, so the PNG
// goes through a temp dir under $TMPDIR that is removed right after
// reading (see the package comment). "all" means the main display: macOS
// numbers displays and there is no single-shot all-displays mode.
func capture(ctx context.Context, req Request) (image.Image, error) {
	args := []string{"-x", "-t", "png"}
	switch {
	case req.Target == TargetWindow:
		r, err := frontWindowBounds(ctx)
		if err != nil {
			return nil, err
		}
		args = append(args, "-R", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.W, r.H))
	case req.Monitor == "current":
		return nil, fmt.Errorf("\"current\" monitor is not supported on macOS; use a monitor number")
	case req.Monitor != "all":
		n, _ := strconv.Atoi(req.Monitor)
		args = append(args, "-D", strconv.Itoa(n))
	}
	img, err := captureToFile(ctx, func(path string) ([]byte, error) {
		return runCommand(ctx, "screencapture", append(args, path)...)
	})
	if err != nil {
		return nil, fmt.Errorf("%w (screencapture needs Screen Recording permission: System Settings > Privacy & Security > Screen Recording)", err)
	}
	return img, nil
}

// osascriptFrontWindow queries System Events for the bounds of the front
// window of the frontmost application: "x, y, w, h".
const osascriptFrontWindow = `tell application "System Events"
	set frontApp to first application process whose frontmost is true
	set frontWin to front window of frontApp
	set {x, y} to position of frontWin
	set {w, h} to size of frontWin
	return (x as text) & ", " & (y as text) & ", " & (w as text) & ", " & (h as text)
end tell`

var frontWindowBoundsRe = regexp.MustCompile(`(-?\d+)\s*,\s*(-?\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

func frontWindowBounds(ctx context.Context) (rect, error) {
	out, err := runCommand(ctx, "osascript", "-e", osascriptFrontWindow)
	if err != nil {
		return rect{}, fmt.Errorf("locate the frontmost window: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	m := frontWindowBoundsRe.FindStringSubmatch(string(out))
	if m == nil {
		return rect{}, fmt.Errorf("cannot parse the frontmost window bounds from %q", strings.TrimSpace(string(out)))
	}
	x, _ := strconv.Atoi(m[1])
	y, _ := strconv.Atoi(m[2])
	w, _ := strconv.Atoi(m[3])
	h, _ := strconv.Atoi(m[4])
	if w <= 0 || h <= 0 {
		return rect{}, fmt.Errorf("the frontmost application has no capturable window")
	}
	return rect{X: x, Y: y, W: w, H: h}, nil
}
