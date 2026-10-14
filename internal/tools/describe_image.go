package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
	"github.com/jonathanhecl/vibe-coder/internal/visionrole"
)

// DescribeImageTool has a vision-capable model look at an image file and
// answer a question about it. It is the model's agency path: automatic
// attachment happens through Read, while this tool asks for a closer look,
// a second opinion, or a follow-up about specific details.
type DescribeImageTool struct {
	router visionRouter
}

func NewDescribeImageTool(cfg *config.Config, client ollama.Client, pool *sidecar.Pool) *DescribeImageTool {
	return &DescribeImageTool{router: visionRouter{cfg: cfg, client: client, pool: pool}}
}

// SetVisionRole installs the optional vision-role describer. A nil describer
// (the default) means no vision role is bound and routing falls back to the
// sidecar and then the main model.
func (t *DescribeImageTool) SetVisionRole(d *visionrole.Describer) *DescribeImageTool {
	t.router.visionRole = d
	return t
}

func (t *DescribeImageTool) Name() string { return "DescribeImage" }
func (t *DescribeImageTool) Description() string {
	return "Have a vision-capable model look at an image file and answer a question about it. Use for a closer look, a second opinion, or follow-up detail on an attached image; a focused question gets a focused answer."
}
func (t *DescribeImageTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Path to a JPEG, PNG, GIF or BMP image, relative to the session working directory (or absolute for files outside it)."},
					"question":  map[string]any{"type": "string", "description": "Optional focused question. When set, the vision model answers only this (e.g. \"Is the Save button visible?\", \"What color is the 'Error' text?\") instead of describing the whole image. Omit for a thorough general description."},
				},
				"required": []string{"file_path"},
			},
		},
	}
}

func (t *DescribeImageTool) Execute(ctx context.Context, params map[string]any) Result {
	path, ok := params["file_path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return errResult("file_path is required")
	}
	path = strings.TrimSpace(path)
	vr := validateExistingFileForRead(path)
	if vr.IsError() {
		return Result{Output: vr.UserError, HintsForModel: vr.AssistantHints, IsError: true}
	}
	if !vision.IsSupportedImage(path) {
		return errResult(fmt.Sprintf("unsupported image format: %s (supported formats: %s)", path, vision.SupportedFormats()))
	}
	if info, err := os.Lstat(path); err != nil {
		return errResult(fmt.Sprintf("read image: %v", err))
	} else if info.Size() > vision.MaxInputBytes {
		return errResult(fmt.Sprintf("image too large: %s (%d bytes, max %d)", path, info.Size(), vision.MaxInputBytes))
	}
	question, _ := params["question"].(string)

	name, desc, err := t.router.ask(ctx, visionInput{path: path, label: filepath.Base(path)}, question)
	if err != nil {
		return errResult(err.Error())
	}
	return Result{Output: fmt.Sprintf("Visual answer from %s about %s:\n%s", name, filepath.Base(path), desc)}
}
