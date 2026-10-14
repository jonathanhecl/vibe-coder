//go:build linux

package screenshot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// fakeBackend stubs lookPath and runCommand so backend selection and
// command lines can be asserted without a real compositor.
type fakeBackend struct {
	installed map[string]bool
	outputs   map[string][]byte // keyed by the command's first token
	calls     []string
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (f *fakeBackend) install(t *testing.T, png []byte) {
	t.Helper()
	oldLook, oldRun := lookPath, runCommand
	lookPath = func(name string) (string, error) {
		if f.installed[name] {
			return "/usr/bin/" + name, nil
		}
		return "", fmt.Errorf("not found")
	}
	runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		if out, ok := f.outputs[name]; ok {
			return out, nil
		}
		return png, nil // default: the backend emits a PNG on stdout
	}
	t.Cleanup(func() { lookPath, runCommand = oldLook, oldRun })
}

func (f *fakeBackend) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func setenv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{"WAYLAND_DISPLAY", "DISPLAY", "HYPRLAND_INSTANCE_SIGNATURE", "SWAYSOCK", "XDG_CURRENT_DESKTOP"} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLinuxBackendHyprland(t *testing.T) {
	png := tinyPNG(t)
	setenv(t, map[string]string{
		"WAYLAND_DISPLAY": "wayland-1", "HYPRLAND_INSTANCE_SIGNATURE": "abc",
		"XDG_CURRENT_DESKTOP": "Hyprland",
	})
	fb := &fakeBackend{
		installed: map[string]bool{"grim": true, "hyprctl": true},
		outputs: map[string][]byte{
			"hyprctl": []byte(`[{"id":0,"name":"eDP-1","focused":true},{"id":1,"name":"HDMI-A-1","focused":false}]`),
		},
	}
	fb.install(t, png)

	// Monitor index 2 → second monitor by id → grim -o HDMI-A-1.
	img, err := Capture(context.Background(), Request{Monitor: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 4 {
		t.Fatalf("unexpected capture size %v", img.Bounds())
	}
	if !fb.ran("grim -o HDMI-A-1 -") {
		t.Fatalf("expected grim -o HDMI-A-1, calls: %v", fb.calls)
	}

	// Active window → hyprctl activewindow -j → grim -g.
	fb.calls = nil
	fb.outputs["hyprctl"] = []byte(`{"at":[10,20],"size":[800,600]}`)
	if _, err := Capture(context.Background(), Request{Target: TargetWindow}); err != nil {
		t.Fatal(err)
	}
	if !fb.ran("grim -g 10,20 800x600 -") {
		t.Fatalf("expected grim -g, calls: %v", fb.calls)
	}
}

func TestLinuxBackendSway(t *testing.T) {
	png := tinyPNG(t)
	setenv(t, map[string]string{
		"WAYLAND_DISPLAY": "wayland-1", "SWAYSOCK": "/run/sway.sock",
		"XDG_CURRENT_DESKTOP": "sway",
	})
	fb := &fakeBackend{
		installed: map[string]bool{"grim": true, "swaymsg": true},
		outputs: map[string][]byte{
			"swaymsg": []byte(`[{"name":"DP-1","focused":false},{"name":"eDP-1","focused":true}]`),
		},
	}
	fb.install(t, png)

	if _, err := Capture(context.Background(), Request{Monitor: "current"}); err != nil {
		t.Fatal(err)
	}
	if !fb.ran("grim -o eDP-1 -") {
		t.Fatalf("expected grim -o eDP-1, calls: %v", fb.calls)
	}

	fb.calls = nil
	fb.outputs["swaymsg"] = []byte(`{"nodes":[{"focused":true,"rect":{"x":1,"y":2,"width":30,"height":40}}]}`)
	if _, err := Capture(context.Background(), Request{Target: TargetWindow}); err != nil {
		t.Fatal(err)
	}
	if !fb.ran("grim -g 1,2 30x40 -") {
		t.Fatalf("expected grim -g, calls: %v", fb.calls)
	}
}

func TestLinuxBackendKDE(t *testing.T) {
	png := tinyPNG(t)
	setenv(t, map[string]string{
		"WAYLAND_DISPLAY": "wayland-1", "XDG_CURRENT_DESKTOP": "KDE",
	})
	fb := &fakeBackend{installed: map[string]bool{"spectacle": true}}
	fb.install(t, png)
	// spectacle writes a file instead of stdout: emulate it.
	oldRun := runCommand
	runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		fb.calls = append(fb.calls, name+" "+strings.Join(args, " "))
		for i, a := range args {
			if a == "-o" && i+1 < len(args) {
				if err := writeFile(args[i+1], png); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	}
	t.Cleanup(func() { runCommand = oldRun })

	if _, err := Capture(context.Background(), Request{Monitor: "current"}); err != nil {
		t.Fatal(err)
	}
	if !fb.ran("spectacle -b -n -m -o ") {
		t.Fatalf("expected spectacle -m, calls: %v", fb.calls)
	}

	if _, err := Capture(context.Background(), Request{Monitor: "2"}); err == nil ||
		!strings.Contains(err.Error(), "not supported with spectacle") {
		t.Fatalf("expected spectacle numeric-monitor error, got %v", err)
	}
}

func TestLinuxBackendGNOME(t *testing.T) {
	png := tinyPNG(t)
	setenv(t, map[string]string{
		"WAYLAND_DISPLAY": "wayland-1", "XDG_CURRENT_DESKTOP": "GNOME",
	})
	fb := &fakeBackend{installed: map[string]bool{"gnome-screenshot": true}}
	fb.install(t, png)
	runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		fb.calls = append(fb.calls, name+" "+strings.Join(args, " "))
		for i, a := range args {
			if a == "-f" && i+1 < len(args) {
				if err := writeFile(args[i+1], png); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	}

	if _, err := Capture(context.Background(), Request{Target: TargetWindow}); err != nil {
		t.Fatal(err)
	}
	if !fb.ran("gnome-screenshot -w -f ") {
		t.Fatalf("expected gnome-screenshot -w, calls: %v", fb.calls)
	}
	if _, err := Capture(context.Background(), Request{Monitor: "2"}); err == nil {
		t.Fatal("expected gnome-screenshot monitor error")
	}
}

func TestLinuxBackendX11(t *testing.T) {
	png := tinyPNG(t)
	setenv(t, map[string]string{"DISPLAY": ":0"})
	fb := &fakeBackend{
		installed: map[string]bool{"import": true, "xdotool": true, "xprop": true, "xrandr": true},
		outputs: map[string][]byte{
			"xdotool": []byte("83886083\n"),
		},
	}
	fb.install(t, png)

	if _, err := Capture(context.Background(), Request{Target: TargetWindow}); err != nil {
		t.Fatal(err)
	}
	if !fb.ran("import -window 83886083 png:-") {
		t.Fatalf("expected import -window <id>, calls: %v", fb.calls)
	}

	// Monitor by number: xrandr geometry, then crop the root capture.
	fb.calls = nil
	fb.outputs["xrandr"] = []byte("Monitors: 1\n 0: +*eDP-1 4/1x4/1+0+0  eDP-1\n")
	if _, err := Capture(context.Background(), Request{Monitor: "1"}); err != nil {
		t.Fatal(err)
	}
	if !fb.ran("xrandr --listmonitors") || !fb.ran("import -window root png:-") {
		t.Fatalf("expected xrandr + root import, calls: %v", fb.calls)
	}
}

func TestLinuxBackendNone(t *testing.T) {
	setenv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-1", "XDG_CURRENT_DESKTOP": "KDE"})
	fb := &fakeBackend{installed: map[string]bool{}}
	fb.install(t, tinyPNG(t))
	if _, err := Capture(context.Background(), Request{}); err == nil ||
		!strings.Contains(err.Error(), "no screenshot backend found") {
		t.Fatalf("expected no-backend error, got %v", err)
	}
}
