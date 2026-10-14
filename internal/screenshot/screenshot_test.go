package screenshot

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      Request
		want    Request
		wantErr string
	}{
		{"defaults", Request{}, Request{Target: TargetScreen, Monitor: "all"}, ""},
		{"screen all", Request{Target: "screen", Monitor: "all"}, Request{Target: TargetScreen, Monitor: "all"}, ""},
		{"current", Request{Target: "screen", Monitor: "current"}, Request{Target: TargetScreen, Monitor: "current"}, ""},
		{"number", Request{Target: "screen", Monitor: "2"}, Request{Target: TargetScreen, Monitor: "2"}, ""},
		{"window", Request{Target: "window"}, Request{Target: TargetWindow}, ""},
		{"bad target", Request{Target: "desktop"}, Request{}, "invalid target"},
		{"zero monitor", Request{Monitor: "0"}, Request{}, "invalid monitor"},
		{"negative monitor", Request{Monitor: "-1"}, Request{}, "invalid monitor"},
		{"text monitor", Request{Monitor: "primary"}, Request{}, "invalid monitor"},
		{"window monitor", Request{Target: "window", Monitor: "1"}, Request{}, "monitor option only applies"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.in.Normalize()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected %q error, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   Request
		want string
	}{
		{Request{Target: TargetScreen, Monitor: "all"}, "all monitors"},
		{Request{Target: TargetScreen, Monitor: ""}, "all monitors"},
		{Request{Target: TargetScreen, Monitor: "current"}, "current monitor"},
		{Request{Target: TargetScreen, Monitor: "2"}, "monitor 2"},
		{Request{Target: TargetWindow}, "active window"},
	}
	for _, tc := range cases {
		if got := tc.in.Label(); got != tc.want {
			t.Fatalf("Label(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseHyprctlMonitors(t *testing.T) {
	t.Parallel()
	data := []byte(`[
		{"id": 1, "name": "HDMI-A-1", "focused": false},
		{"id": 0, "name": "eDP-1", "focused": true}
	]`)
	// Sorted by id: eDP-1 (0) is first, HDMI-A-1 (1) second.
	name, err := hyprctlMonitorByIndex(data, 2)
	if err != nil || name != "HDMI-A-1" {
		t.Fatalf("expected HDMI-A-1, got %q (%v)", name, err)
	}
	if _, err := hyprctlMonitorByIndex(data, 3); err == nil {
		t.Fatal("expected out-of-range error")
	}
	name, err = hyprctlFocusedMonitor(data)
	if err != nil || name != "eDP-1" {
		t.Fatalf("expected eDP-1 focused, got %q (%v)", name, err)
	}
	if _, err := hyprctlFocusedMonitor([]byte(`[{"id":0,"name":"x","focused":false}]`)); err == nil {
		t.Fatal("expected no-focused-monitor error")
	}
}

func TestParseHyprctlActiveWindow(t *testing.T) {
	t.Parallel()
	r, err := parseHyprctlActiveWindow([]byte(`{"at": [10, 20], "size": [800, 600]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r != (rect{X: 10, Y: 20, W: 800, H: 600}) {
		t.Fatalf("unexpected rect %+v", r)
	}
	if _, err := parseHyprctlActiveWindow([]byte(`{}`)); err == nil {
		t.Fatal("expected error for missing window")
	}
}

func TestParseSwayOutputs(t *testing.T) {
	t.Parallel()
	// Array order is kept (unlike hyprctl which is sorted by id).
	data := []byte(`[
		{"name": "DP-2", "focused": false},
		{"name": "eDP-1", "focused": true}
	]`)
	name, err := swayOutputByIndex(data, 1)
	if err != nil || name != "DP-2" {
		t.Fatalf("expected DP-2, got %q (%v)", name, err)
	}
	name, err = swayFocusedOutput(data)
	if err != nil || name != "eDP-1" {
		t.Fatalf("expected eDP-1, got %q (%v)", name, err)
	}
}

func TestParseSwayFocusedRect(t *testing.T) {
	t.Parallel()
	tree := []byte(`{"nodes": [
		{"nodes": [{"focused": false, "rect": {"x":0,"y":0,"width":1,"height":1}}]},
		{"floating_nodes": [{"focused": true, "rect": {"x":5,"y":6,"width":70,"height":80}}]}
	]}`)
	r, err := parseSwayFocusedRect(tree)
	if err != nil {
		t.Fatal(err)
	}
	if r != (rect{X: 5, Y: 6, W: 70, H: 80}) {
		t.Fatalf("unexpected rect %+v", r)
	}
	if _, err := parseSwayFocusedRect([]byte(`{"nodes": []}`)); err == nil {
		t.Fatal("expected no-focused-node error")
	}
}

func TestParseXrandrMonitors(t *testing.T) {
	t.Parallel()
	data := []byte(`Monitors: 2
 0: +*eDP-1 1920/527x1080/296+0+0  eDP-1
 1: +HDMI-A-1 2560/597x1440/336+1920+0  HDMI-A-1
`)
	mons, err := parseXrandrMonitors(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(mons) != 2 {
		t.Fatalf("expected 2 monitors, got %d", len(mons))
	}
	if mons[0] != (rect{X: 0, Y: 0, W: 1920, H: 1080}) || mons[1] != (rect{X: 1920, Y: 0, W: 2560, H: 1440}) {
		t.Fatalf("unexpected rects %+v", mons)
	}
	if idx := monitorAt(mons, 2000, 10); idx != 1 {
		t.Fatalf("expected monitor 1 at (2000,10), got %d", idx)
	}
	if idx := monitorAt(mons, -5, 10); idx != -1 {
		t.Fatalf("expected no monitor at (-5,10), got %d", idx)
	}
	if _, err := parseXrandrMonitors([]byte("Monitors: 0")); err == nil {
		t.Fatal("expected no-monitors error")
	}
}

func TestParseXpropActiveWindow(t *testing.T) {
	t.Parallel()
	id, err := parseXpropActiveWindow([]byte("_NET_ACTIVE_WINDOW(WINDOW): window id # 0x3e00007"))
	if err != nil || id != "0x3e00007" {
		t.Fatalf("expected 0x3e00007, got %q (%v)", id, err)
	}
	if _, err := parseXpropActiveWindow([]byte("_NET_ACTIVE_WINDOW(WINDOW): window id # 0x0")); err == nil {
		t.Fatal("expected no-window error")
	}
	if _, err := parseXpropActiveWindow([]byte("_NET_ACTIVE_WINDOW: not found")); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseXdotoolMouseLocation(t *testing.T) {
	t.Parallel()
	x, y, err := parseXdotoolMouseLocation([]byte("X=2100\nY=300\nSCREEN=0\nWINDOW=12345\n"))
	if err != nil || x != 2100 || y != 300 {
		t.Fatalf("expected (2100,300), got (%d,%d) (%v)", x, y, err)
	}
	if _, _, err := parseXdotoolMouseLocation([]byte("garbage")); err == nil {
		t.Fatal("expected parse error")
	}
}
