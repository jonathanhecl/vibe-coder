//go:build windows

package screenshot

import (
	"context"
	"fmt"
	"image"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procMonitorFromPoint              = user32.NewProc("MonitorFromPoint")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procGetForegroundWindow           = user32.NewProc("GetForegroundWindow")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC            = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC                      = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap        = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject                  = gdi32.NewProc("SelectObject")
	procDeleteObject                  = gdi32.NewProc("DeleteObject")
	procBitBlt                        = gdi32.NewProc("BitBlt")
	procGetDIBits                     = gdi32.NewProc("GetDIBits")
	procDwmGetWindowAttribute         = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	monitorDefaultToNearest = 2

	dwmaExtendedFrameBounds = 9

	srcCopy      = 0x00CC0020
	captureBlt   = 0x40000000
	biRGB        = 0
	dibRGBColors = 0
)

type winPoint struct{ X, Y int32 }
type winRect struct{ Left, Top, Right, Bottom int32 }

type monitorInfo struct {
	CbSize    uint32
	RcMonitor winRect
	RcWork    winRect
	DwFlags   uint32
}

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

var dpiOnce sync.Once

// makeDPIAware switches the process to per-monitor DPI awareness once, so
// coordinates and pixels line up on scaled displays.
func makeDPIAware() {
	dpiOnce.Do(func() {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4.
		if procSetProcessDpiAwarenessContext.Find() == nil {
			if r, _, _ := procSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 {
				return
			}
		}
		procSetProcessDPIAware.Call()
	})
}

func systemMetric(idx int32) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(idx))
	return int(int32(r))
}

func capture(ctx context.Context, req Request) (image.Image, error) {
	makeDPIAware()
	if req.Target == TargetWindow {
		hwnd, _, _ := procGetForegroundWindow.Call()
		if hwnd == 0 {
			return nil, fmt.Errorf("no foreground window to capture")
		}
		r, err := windowFrameRect(windows.Handle(hwnd))
		if err != nil {
			return nil, err
		}
		return captureRect(image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)))
	}
	switch req.Monitor {
	case "all":
		x := systemMetric(smXVirtualScreen)
		y := systemMetric(smYVirtualScreen)
		w := systemMetric(smCXVirtualScreen)
		h := systemMetric(smCYVirtualScreen)
		if w <= 0 || h <= 0 {
			return nil, fmt.Errorf("the virtual screen has no extents")
		}
		return captureRect(image.Rect(x, y, x+w, y+h))
	case "current":
		var pt winPoint
		if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
			return nil, fmt.Errorf("GetCursorPos failed")
		}
		mon, _, _ := procMonitorFromPoint.Call(uintptr(pt.X), uintptr(pt.Y), monitorDefaultToNearest)
		info, err := monitorRect(mon)
		if err != nil {
			return nil, err
		}
		return captureRect(info)
	default:
		n, _ := strconv.Atoi(req.Monitor)
		mons, err := enumMonitors()
		if err != nil {
			return nil, err
		}
		if n < 1 || n > len(mons) {
			return nil, fmt.Errorf("monitor %d out of range: %d monitor(s) connected", n, len(mons))
		}
		return captureRect(mons[n-1])
	}
}

// enumMonitors returns monitor rectangles in enumeration order.
func enumMonitors() ([]image.Rectangle, error) {
	var out []image.Rectangle
	cb := windows.NewCallback(func(hMon, hdc uintptr, rc *winRect, lparam uintptr) uintptr {
		r, err := monitorRect(hMon)
		if err == nil {
			out = append(out, r)
		}
		return 1 // continue enumeration
	})
	if r, _, err := procEnumDisplayMonitors.Call(0, 0, cb, 0); r == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors failed: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no monitors found")
	}
	return out, nil
}

func monitorRect(hMon uintptr) (image.Rectangle, error) {
	info := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	if r, _, err := procGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&info))); r == 0 {
		return image.Rectangle{}, fmt.Errorf("GetMonitorInfoW failed: %w", err)
	}
	rc := info.RcMonitor
	return image.Rect(int(rc.Left), int(rc.Top), int(rc.Right), int(rc.Bottom)), nil
}

// windowFrameRect returns the window's extended frame bounds (the visible
// rect including the real border, excluding the invisible resize margin),
// falling back to GetWindowRect when DWM is unavailable.
func windowFrameRect(hwnd windows.Handle) (winRect, error) {
	var r winRect
	if err := procDwmGetWindowAttribute.Find(); err == nil {
		ret, _, _ := procDwmGetWindowAttribute.Call(
			uintptr(hwnd), dwmaExtendedFrameBounds,
			uintptr(unsafe.Pointer(&r)), uintptr(unsafe.Sizeof(r)))
		if ret == 0 {
			return r, nil
		}
	}
	if res, _, err := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r))); res == 0 {
		return winRect{}, fmt.Errorf("GetWindowRect failed: %w", err)
	}
	return r, nil
}

// captureRect BitBlts the screen rectangle into an in-memory 32bpp bitmap.
// All GDI handles are released before returning.
func captureRect(r image.Rectangle) (image.Image, error) {
	w, h := r.Dx(), r.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("capture area is empty (%dx%d)", w, h)
	}
	srcDC, _, err := procGetDC.Call(0)
	if srcDC == 0 {
		return nil, fmt.Errorf("GetDC failed: %w", err)
	}
	defer procReleaseDC.Call(0, srcDC)

	memDC, _, err := procCreateCompatibleDC.Call(srcDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed: %w", err)
	}
	defer procDeleteDC.Call(memDC)

	bmp, _, err := procCreateCompatibleBitmap.Call(srcDC, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap failed: %w", err)
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, old)

	if res, _, err := procBitBlt.Call(
		memDC, 0, 0, uintptr(w), uintptr(h),
		srcDC, uintptr(int32(r.Min.X)), uintptr(int32(r.Min.Y)),
		srcCopy|captureBlt); res == 0 {
		return nil, fmt.Errorf("BitBlt failed: %w", err)
	}

	// Negative height asks for a top-down DIB so pixel rows arrive in
	// display order.
	bih := bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       int32(w),
		BiHeight:      -int32(h),
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: biRGB,
	}
	pixels := make([]byte, w*h*4)
	res, _, err := procGetDIBits.Call(
		memDC, bmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bih)), dibRGBColors)
	if res == 0 {
		return nil, fmt.Errorf("GetDIBits failed: %w", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+3 < len(pixels); i += 4 {
		// DIB stores BGRA; RGBA wants the channels swapped.
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
		pixels[i+3] = 0xFF
	}
	copy(img.Pix, pixels)
	return img, nil
}
