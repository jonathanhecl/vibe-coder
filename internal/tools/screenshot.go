package tools

import (
	"context"
	"fmt"
	"image"
	"strconv"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/screenshot"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
	"github.com/jonathanhecl/vibe-coder/internal/visionrole"
)

// ScreenshotTool captures a monitor or the active window and has a
// vision-capable model answer a question about it. The capture lives only
// in memory: it is encoded straight into the vision request and never
// saved to disk, the session, or the transcript.
type ScreenshotTool struct {
	router  visionRouter
	capture func(context.Context, screenshot.Request) (image.Image, error)
}

func NewScreenshotTool(cfg *config.Config, client ollama.Client, pool *sidecar.Pool) *ScreenshotTool {
	return &ScreenshotTool{
		router:  visionRouter{cfg: cfg, client: client, pool: pool},
		capture: screenshot.Capture,
	}
}

// SetVisionRole installs the optional vision-role describer, same routing
// as DescribeImage.
func (t *ScreenshotTool) SetVisionRole(d *visionrole.Describer) *ScreenshotTool {
	t.router.visionRole = d
	return t
}

func (t *ScreenshotTool) Name() string { return "Screenshot" }
func (t *ScreenshotTool) Description() string {
	return "Capture the screen or the active window and have a vision model answer a question about it. Use it to verify visual results of your work (a UI you changed, an app window). The capture stays in memory and is never saved."
}
func (t *ScreenshotTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"enum":        []string{"screen", "window"},
						"description": "What to capture: the screen (a monitor) or the currently active window. Default \"screen\".",
					},
					"monitor": map[string]any{
						"type":        "string",
						"description": "Screen only: \"all\" (default) captures every monitor, \"current\" captures the monitor under the cursor/focused, or a 1-based monitor number like \"2\".",
					},
					"question": map[string]any{
						"type":        "string",
						"description": "Optional focused question. When set, the vision model answers only this (e.g. \"Is the Save button visible?\", \"What color is the 'Error' text?\") instead of describing the whole image. Omit for a thorough general description.",
					},
				},
			},
		},
	}
}

func (t *ScreenshotTool) Execute(ctx context.Context, params map[string]any) Result {
	req := screenshot.Request{Target: screenshot.Target(stringParam(params, "target"))}
	switch v := params["monitor"].(type) {
	case string:
		req.Monitor = v
	case float64:
		// JSON numbers arrive as float64; a monitor index is an integer.
		req.Monitor = strconv.Itoa(int(v))
	}
	norm, err := req.Normalize()
	if err != nil {
		return errResult(err.Error())
	}
	question, _ := params["question"].(string)

	img, err := t.capture(ctx, norm)
	if err != nil {
		return errResult(fmt.Sprintf("screenshot failed: %v", err))
	}
	payload, err := vision.EncodeImage(img, vision.ScreenshotMaxDimension)
	if err != nil {
		return errResult(fmt.Sprintf("screenshot failed: %v", err))
	}
	label := "screenshot (" + norm.Label() + ")"
	name, desc, err := t.router.ask(ctx, visionInput{payload: payload, label: label}, question)
	if err != nil {
		return errResult(err.Error())
	}
	bounds := img.Bounds()
	return Result{Output: fmt.Sprintf("Visual answer from %s about screenshot (%s, %dx%d):\n%s",
		name, norm.Label(), bounds.Dx(), bounds.Dy(), desc)}
}

func stringParam(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}
