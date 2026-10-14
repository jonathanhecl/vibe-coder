package screenshot

// Parsers for the external capture helpers live here, free of build tags,
// so they are exercised by tests on every platform.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// rect is a pixel rectangle from a backend.
type rect struct {
	X, Y, W, H int
}

// hyprMonitor is one entry of `hyprctl monitors -j`.
type hyprMonitor struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Focused bool   `json:"focused"`
}

// parseHyprctlMonitors decodes `hyprctl monitors -j`.
func parseHyprctlMonitors(data []byte) ([]hyprMonitor, error) {
	var mons []hyprMonitor
	if err := json.Unmarshal(data, &mons); err != nil {
		return nil, fmt.Errorf("parse hyprctl monitors output: %w", err)
	}
	return mons, nil
}

// hyprctlMonitorByIndex returns the output name of the 1-based Nth monitor
// sorted by id.
func hyprctlMonitorByIndex(data []byte, n int) (string, error) {
	mons, err := parseHyprctlMonitors(data)
	if err != nil {
		return "", err
	}
	sort.Slice(mons, func(i, j int) bool { return mons[i].ID < mons[j].ID })
	if n < 1 || n > len(mons) {
		return "", fmt.Errorf("monitor %d out of range: %d monitor(s) connected", n, len(mons))
	}
	return mons[n-1].Name, nil
}

// hyprctlFocusedMonitor returns the name of the focused monitor.
func hyprctlFocusedMonitor(data []byte) (string, error) {
	mons, err := parseHyprctlMonitors(data)
	if err != nil {
		return "", err
	}
	for _, m := range mons {
		if m.Focused {
			return m.Name, nil
		}
	}
	return "", fmt.Errorf("no focused monitor reported by hyprctl")
}

// parseHyprctlActiveWindow decodes `hyprctl activewindow -j` into a rect.
func parseHyprctlActiveWindow(data []byte) (rect, error) {
	var win struct {
		At   [2]int `json:"at"`
		Size [2]int `json:"size"`
	}
	if err := json.Unmarshal(data, &win); err != nil {
		return rect{}, fmt.Errorf("parse hyprctl activewindow output: %w", err)
	}
	if win.Size[0] <= 0 || win.Size[1] <= 0 {
		return rect{}, fmt.Errorf("hyprctl reported no active window")
	}
	return rect{X: win.At[0], Y: win.At[1], W: win.Size[0], H: win.Size[1]}, nil
}

// swayOutput is one entry of `swaymsg -t get_outputs`.
type swayOutput struct {
	Name    string `json:"name"`
	Focused bool   `json:"focused"`
}

// parseSwayOutputs decodes `swaymsg -t get_outputs`, keeping array order.
func parseSwayOutputs(data []byte) ([]swayOutput, error) {
	var outs []swayOutput
	if err := json.Unmarshal(data, &outs); err != nil {
		return nil, fmt.Errorf("parse swaymsg outputs: %w", err)
	}
	return outs, nil
}

func swayOutputByIndex(data []byte, n int) (string, error) {
	outs, err := parseSwayOutputs(data)
	if err != nil {
		return "", err
	}
	if n < 1 || n > len(outs) {
		return "", fmt.Errorf("monitor %d out of range: %d monitor(s) connected", n, len(outs))
	}
	return outs[n-1].Name, nil
}

func swayFocusedOutput(data []byte) (string, error) {
	outs, err := parseSwayOutputs(data)
	if err != nil {
		return "", err
	}
	for _, o := range outs {
		if o.Focused {
			return o.Name, nil
		}
	}
	return "", fmt.Errorf("no focused output reported by swaymsg")
}

// swayNode is the recursive shape of `swaymsg -t get_tree`.
type swayNode struct {
	Focused bool `json:"focused"`
	Rect    struct {
		X, Y, Width, Height int
	} `json:"rect"`
	Nodes         []swayNode `json:"nodes"`
	FloatingNodes []swayNode `json:"floating_nodes"`
}

// parseSwayFocusedRect finds the focused node in the sway tree and returns
// its rectangle.
func parseSwayFocusedRect(data []byte) (rect, error) {
	var root swayNode
	if err := json.Unmarshal(data, &root); err != nil {
		return rect{}, fmt.Errorf("parse swaymsg tree: %w", err)
	}
	if r, ok := findFocusedSwayNode(root); ok {
		return r, nil
	}
	return rect{}, fmt.Errorf("no focused window in the sway tree")
}

func findFocusedSwayNode(n swayNode) (rect, bool) {
	if n.Focused && n.Rect.Width > 0 && n.Rect.Height > 0 {
		return rect{X: n.Rect.X, Y: n.Rect.Y, W: n.Rect.Width, H: n.Rect.Height}, true
	}
	for _, child := range n.Nodes {
		if r, ok := findFocusedSwayNode(child); ok {
			return r, true
		}
	}
	for _, child := range n.FloatingNodes {
		if r, ok := findFocusedSwayNode(child); ok {
			return r, true
		}
	}
	return rect{}, false
}

// xrandrMonitorRe matches one `xrandr --listmonitors` geometry field, e.g.
// "1920/527x1080/296+0+0".
var xrandrMonitorRe = regexp.MustCompile(`(\d+)/\d+x(\d+)/\d+\+(-?\d+)\+(-?\d+)`)

// parseXrandrMonitors returns monitor rectangles in list order.
func parseXrandrMonitors(data []byte) ([]rect, error) {
	var out []rect
	for _, line := range strings.Split(string(data), "\n") {
		m := xrandrMonitorRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		x, _ := strconv.Atoi(m[3])
		y, _ := strconv.Atoi(m[4])
		out = append(out, rect{X: x, Y: y, W: w, H: h})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no monitors found in xrandr output")
	}
	return out, nil
}

// xpropActiveWindowRe matches the window id in
// "_NET_ACTIVE_WINDOW(WINDOW): window id # 0x3e00007".
var xpropActiveWindowRe = regexp.MustCompile(`#\s*(0x[0-9a-fA-F]+|\d+)`)

// parseXpropActiveWindow extracts the active window id from xprop output.
func parseXpropActiveWindow(data []byte) (string, error) {
	m := xpropActiveWindowRe.FindStringSubmatch(string(data))
	if m == nil {
		return "", fmt.Errorf("no active window id in xprop output")
	}
	if m[1] == "0x0" || m[1] == "0" {
		return "", fmt.Errorf("no active window reported by xprop")
	}
	return m[1], nil
}

// parseXdotoolMouseLocation decodes `xdotool getmouselocation --shell`.
func parseXdotoolMouseLocation(data []byte) (x, y int, err error) {
	var xs, ys string
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "X="); ok {
			xs = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "Y="); ok {
			ys = strings.TrimSpace(v)
		}
	}
	if xs == "" || ys == "" {
		return 0, 0, fmt.Errorf("no mouse coordinates in xdotool output")
	}
	x, err = strconv.Atoi(xs)
	if err != nil {
		return 0, 0, fmt.Errorf("parse xdotool X coordinate: %w", err)
	}
	y, err = strconv.Atoi(ys)
	if err != nil {
		return 0, 0, fmt.Errorf("parse xdotool Y coordinate: %w", err)
	}
	return x, y, nil
}

// monitorAt returns the index of the monitor containing (x,y), or -1.
func monitorAt(mons []rect, x, y int) int {
	for i, m := range mons {
		if x >= m.X && x < m.X+m.W && y >= m.Y && y < m.Y+m.H {
			return i
		}
	}
	return -1
}
