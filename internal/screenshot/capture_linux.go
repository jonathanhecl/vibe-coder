//go:build linux

package screenshot

import (
	"context"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
)

const noBackendError = "no screenshot backend found; install one of: grim (wlroots: Sway/Hyprland), spectacle (KDE), gnome-screenshot (GNOME), or ImageMagick import (X11)"

func capture(ctx context.Context, req Request) (image.Image, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return captureWayland(ctx, req)
	}
	if os.Getenv("DISPLAY") != "" {
		return captureX11(ctx, req)
	}
	return nil, fmt.Errorf(noBackendError)
}

func hasCmd(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := runCommand(ctx, name, args...)
	if err != nil {
		return out, fmt.Errorf("%s failed: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func captureWayland(ctx context.Context, req Request) (image.Image, error) {
	grim := hasCmd("grim")
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" && grim:
		return captureHyprland(ctx, req)
	case os.Getenv("SWAYSOCK") != "" && grim:
		return captureSway(ctx, req)
	case strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "KDE") && hasCmd("spectacle"):
		return captureSpectacle(ctx, req)
	case strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "GNOME") && hasCmd("gnome-screenshot"):
		return captureGnome(ctx, req)
	case grim:
		// A generic wlroots compositor: grim can grab the full output set
		// but has no standard way to address one monitor or the active
		// window.
		if req.Target == TargetScreen && req.Monitor == "all" {
			out, err := run(ctx, "grim", "-")
			if err != nil {
				return nil, err
			}
			return decodePNG(out)
		}
		return nil, fmt.Errorf("grim cannot select a monitor or the active window on this compositor; supported compositors: Sway, Hyprland")
	default:
		return nil, fmt.Errorf(noBackendError)
	}
}

// captureHyprland uses grim plus hyprctl for monitor/window geometry.
func captureHyprland(ctx context.Context, req Request) (image.Image, error) {
	if req.Target == TargetWindow {
		out, err := run(ctx, "hyprctl", "activewindow", "-j")
		if err != nil {
			return nil, err
		}
		r, err := parseHyprctlActiveWindow(out)
		if err != nil {
			return nil, err
		}
		return grim(ctx, "-g", fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.W, r.H))
	}
	switch req.Monitor {
	case "all":
		return grim(ctx)
	case "current":
		out, err := run(ctx, "hyprctl", "monitors", "-j")
		if err != nil {
			return nil, err
		}
		name, err := hyprctlFocusedMonitor(out)
		if err != nil {
			return nil, err
		}
		return grim(ctx, "-o", name)
	default:
		n, _ := strconv.Atoi(req.Monitor)
		out, err := run(ctx, "hyprctl", "monitors", "-j")
		if err != nil {
			return nil, err
		}
		name, err := hyprctlMonitorByIndex(out, n)
		if err != nil {
			return nil, err
		}
		return grim(ctx, "-o", name)
	}
}

// captureSway uses grim plus swaymsg for monitor/window geometry.
func captureSway(ctx context.Context, req Request) (image.Image, error) {
	if req.Target == TargetWindow {
		out, err := run(ctx, "swaymsg", "-t", "get_tree")
		if err != nil {
			return nil, err
		}
		r, err := parseSwayFocusedRect(out)
		if err != nil {
			return nil, err
		}
		return grim(ctx, "-g", fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.W, r.H))
	}
	switch req.Monitor {
	case "all":
		return grim(ctx)
	case "current":
		out, err := run(ctx, "swaymsg", "-t", "get_outputs")
		if err != nil {
			return nil, err
		}
		name, err := swayFocusedOutput(out)
		if err != nil {
			return nil, err
		}
		return grim(ctx, "-o", name)
	default:
		n, _ := strconv.Atoi(req.Monitor)
		out, err := run(ctx, "swaymsg", "-t", "get_outputs")
		if err != nil {
			return nil, err
		}
		name, err := swayOutputByIndex(out, n)
		if err != nil {
			return nil, err
		}
		return grim(ctx, "-o", name)
	}
}

func grim(ctx context.Context, args ...string) (image.Image, error) {
	out, err := run(ctx, "grim", append(args, "-")...)
	if err != nil {
		return nil, err
	}
	return decodePNG(out)
}

// captureSpectacle drives KDE's spectacle. It writes only to files, so the
// PNG goes through a temp dir that lives on tmpfs when possible and is
// removed right after reading (see the package comment).
func captureSpectacle(ctx context.Context, req Request) (image.Image, error) {
	args := []string{"-b", "-n"}
	switch {
	case req.Target == TargetWindow:
		args = append(args, "-a")
	case req.Monitor == "current":
		args = append(args, "-m")
	case req.Monitor != "all":
		return nil, fmt.Errorf("selecting a monitor by number is not supported with spectacle; use monitor \"current\" or \"all\"")
	default:
		args = append(args, "-f")
	}
	return captureToFile(ctx, func(path string) ([]byte, error) {
		return run(ctx, "spectacle", append(args, "-o", path)...)
	})
}

// captureGnome drives gnome-screenshot, also file-only.
func captureGnome(ctx context.Context, req Request) (image.Image, error) {
	var args []string
	switch {
	case req.Target == TargetWindow:
		args = append(args, "-w")
	case req.Monitor != "all":
		return nil, fmt.Errorf("selecting a monitor is not supported with gnome-screenshot; it captures all monitors or the active window")
	}
	return captureToFile(ctx, func(path string) ([]byte, error) {
		return run(ctx, "gnome-screenshot", append(args, "-f", path)...)
	})
}

// captureX11 uses ImageMagick `import`, which writes PNG to stdout. Monitor
// sub-rectangles are cropped in Go from the root-window capture.
func captureX11(ctx context.Context, req Request) (image.Image, error) {
	if !hasCmd("import") {
		return nil, fmt.Errorf(noBackendError)
	}
	if req.Target == TargetWindow {
		id, err := x11ActiveWindow(ctx)
		if err != nil {
			return nil, err
		}
		out, err := run(ctx, "import", "-window", id, "png:-")
		if err != nil {
			return nil, err
		}
		return decodePNG(out)
	}
	if req.Monitor == "all" {
		out, err := run(ctx, "import", "-window", "root", "png:-")
		if err != nil {
			return nil, err
		}
		return decodePNG(out)
	}
	// One monitor: crop the root capture using xrandr geometry.
	mons, err := x11Monitors(ctx)
	if err != nil {
		return nil, err
	}
	var idx int
	if req.Monitor == "current" {
		if !hasCmd("xdotool") {
			return nil, fmt.Errorf("capturing the current monitor needs xdotool to locate the pointer; install xdotool or pick a monitor number")
		}
		out, err := run(ctx, "xdotool", "getmouselocation", "--shell")
		if err != nil {
			return nil, err
		}
		x, y, err := parseXdotoolMouseLocation(out)
		if err != nil {
			return nil, err
		}
		idx = monitorAt(mons, x, y)
		if idx < 0 {
			return nil, fmt.Errorf("the pointer is not on any monitor reported by xrandr")
		}
	} else {
		n, _ := strconv.Atoi(req.Monitor)
		if n < 1 || n > len(mons) {
			return nil, fmt.Errorf("monitor %d out of range: %d monitor(s) connected", n, len(mons))
		}
		idx = n - 1
	}
	out, err := run(ctx, "import", "-window", "root", "png:-")
	if err != nil {
		return nil, err
	}
	img, err := decodePNG(out)
	if err != nil {
		return nil, err
	}
	r := mons[idx]
	bounds := img.Bounds()
	crop := image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H).Intersect(bounds)
	if crop.Empty() {
		return nil, fmt.Errorf("monitor %d geometry %dx%d+%d+%d does not overlap the screen capture", idx+1, r.W, r.H, r.X, r.Y)
	}
	dst := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	for y := 0; y < crop.Dy(); y++ {
		for x := 0; x < crop.Dx(); x++ {
			dst.Set(x, y, img.At(crop.Min.X+x, crop.Min.Y+y))
		}
	}
	return dst, nil
}

func x11Monitors(ctx context.Context) ([]rect, error) {
	if !hasCmd("xrandr") {
		return nil, fmt.Errorf("selecting a monitor needs xrandr to list monitor geometry; install xrandr or use monitor \"all\"")
	}
	out, err := run(ctx, "xrandr", "--listmonitors")
	if err != nil {
		return nil, err
	}
	return parseXrandrMonitors(out)
}

func x11ActiveWindow(ctx context.Context) (string, error) {
	if hasCmd("xdotool") {
		out, err := runCommand(ctx, "xdotool", "getactivewindow")
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return strings.TrimSpace(string(out)), nil
		}
	}
	if hasCmd("xprop") {
		out, err := runCommand(ctx, "xprop", "-root", "_NET_ACTIVE_WINDOW")
		if err == nil {
			return parseXpropActiveWindow(out)
		}
	}
	return "", fmt.Errorf("cannot find the active window: install xdotool or xprop")
}
