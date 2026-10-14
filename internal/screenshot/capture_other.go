//go:build !linux && !windows && !darwin

package screenshot

import (
	"context"
	"fmt"
	"image"
	"runtime"
)

func capture(ctx context.Context, req Request) (image.Image, error) {
	return nil, fmt.Errorf("screenshots are not supported on %s", runtime.GOOS)
}
