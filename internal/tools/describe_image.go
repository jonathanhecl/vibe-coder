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
)

// DescribeImageTool has a vision-capable model look at an image file and
// answer a question about it. It is the model's agency path: automatic
// attachment happens through Read, while this tool asks for a closer look,
// a second opinion, or a follow-up about specific details.
//
// Routing: the sidecar answers when it has (or may have) vision, so the
// main model stays focused; otherwise the main model looks itself; when
// neither can see, the call fails with a clear error instead of guessing.
type DescribeImageTool struct {
	cfg    *config.Config
	client ollama.Client
	pool   *sidecar.Pool
}

func NewDescribeImageTool(cfg *config.Config, client ollama.Client, pool *sidecar.Pool) *DescribeImageTool {
	return &DescribeImageTool{cfg: cfg, client: client, pool: pool}
}

func (t *DescribeImageTool) Name() string { return "DescribeImage" }
func (t *DescribeImageTool) Description() string {
	return "Have a vision-capable model look at an image file and answer a question about it. Use for a closer look, a second opinion, or follow-up detail on an attached image."
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
					"file_path": map[string]any{"type": "string", "description": "Absolute path to a JPEG, PNG, GIF or BMP image."},
					"question":  map[string]any{"type": "string", "description": "What to look for. Defaults to a thorough general description."},
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
	if strings.TrimSpace(question) == "" {
		question = sidecar.DefaultDescribeQuestion
	}

	if t.sidecarMaySee() {
		desc, err := t.pool.DescribeImage(ctx, path, question)
		if err != nil {
			return errResult(fmt.Sprintf("sidecar vision failed: %v", err))
		}
		return Result{Output: fmt.Sprintf("Visual answer from %s about %s:\n%s", t.cfg.SidecarModel, filepath.Base(path), desc)}
	}
	if t.mainMaySee() {
		desc, err := describeWithMain(ctx, t.client, t.cfg, path, question)
		if err != nil {
			return errResult(fmt.Sprintf("vision failed: %v", err))
		}
		return Result{Output: fmt.Sprintf("Visual answer from %s about %s:\n%s", t.cfg.Model, filepath.Base(path), desc)}
	}
	return errResult(fmt.Sprintf("neither the main model (%s) nor the sidecar (%s) has vision capability; cannot look at images", t.cfg.Model, t.cfg.SidecarModel))
}

// sidecarMaySee reports whether the sidecar route is worth trying: enabled
// and known-capable, or enabled with unknown capability (honest attempt).
func (t *DescribeImageTool) sidecarMaySee() bool {
	if t == nil || t.cfg == nil || t.pool == nil || !t.pool.Enabled() {
		return false
	}
	return t.cfg.SidecarVisionAvailable || !t.cfg.SidecarVisionKnown
}

func (t *DescribeImageTool) mainMaySee() bool {
	if t == nil || t.cfg == nil || t.client == nil {
		return false
	}
	return t.cfg.VisionAvailable || !t.cfg.VisionKnown
}

// describeWithMain is the fallback when the sidecar cannot see: the main
// model looks itself. Timeouts mirror the sidecar call budget.
func describeWithMain(ctx context.Context, client ollama.Client, cfg *config.Config, path, question string) (string, error) {
	payload, err := vision.EncodeFile(path)
	if err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, sidecar.CallTimeout)
	defer cancel()
	resp, err := client.ChatSync(callCtx, ollama.ChatRequest{
		Model: cfg.Model,
		Messages: []ollama.Message{
			{Role: "system", Content: sidecar.VisualAssistantSystem},
			{Role: "user", Content: fmt.Sprintf("%s\n\n[image file: %s]", question, filepath.Base(path)), Images: []string{payload}},
		},
		Options: ollama.ChatOptions{Temperature: 0, NumPredict: 512},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("model returned empty description")
	}
	return resp.Content, nil
}
