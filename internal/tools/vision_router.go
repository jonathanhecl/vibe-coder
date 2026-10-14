package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
	"github.com/jonathanhecl/vibe-coder/internal/visionrole"
)

// visionInput is one image to look at: either a file on disk (path set, so
// the sidecar and vision-role caches apply) or an in-memory base64 payload
// with a human-readable label (screenshots).
type visionInput struct {
	path    string
	payload string
	label   string
}

// visionRouter shares the vision call routing between DescribeImage (file
// inputs) and Screenshot (in-memory inputs). Order: a model bound to the
// vision role answers first; then the sidecar when it may see, so the main
// model stays focused; then the main model itself; otherwise a clear error.
type visionRouter struct {
	cfg        *config.Config
	client     ollama.Client
	pool       *sidecar.Pool
	visionRole *visionrole.Describer
}

// ask routes the image through the first capable model and returns the
// answering model's name with its answer.
func (r *visionRouter) ask(ctx context.Context, in visionInput, question string) (string, string, error) {
	if strings.TrimSpace(question) == "" {
		question = sidecar.DefaultDescribeQuestion
	}
	if r.visionRole.Enabled() {
		name := r.visionRole.Model()
		desc, err := r.describeWithRole(ctx, in, question)
		if err == nil {
			return name, desc, nil
		}
		if r.mainMaySee() {
			descMain, errMain := r.describeWithMain(ctx, in, question)
			if errMain == nil {
				return r.cfg.Model, descMain, nil
			}
			return "", "", fmt.Errorf("vision role (%s) failed: %v, and main model fallback failed: %v", name, err, errMain)
		}
		return "", "", fmt.Errorf("vision role (%s) failed: %v", name, err)
	}
	if r.sidecarMaySee() {
		desc, err := r.describeWithSidecar(ctx, in, question)
		if err == nil {
			return r.cfg.SidecarModel, desc, nil
		}
		if r.mainMaySee() {
			descMain, errMain := r.describeWithMain(ctx, in, question)
			if errMain == nil {
				return r.cfg.Model, descMain, nil
			}
			return "", "", fmt.Errorf("sidecar vision failed (%v), and main model fallback failed: %v", err, errMain)
		}
		return "", "", fmt.Errorf("sidecar vision failed: %v", err)
	}
	if r.mainMaySee() {
		desc, err := r.describeWithMain(ctx, in, question)
		if err != nil {
			return "", "", fmt.Errorf("vision failed: %v", err)
		}
		return r.cfg.Model, desc, nil
	}
	return "", "", fmt.Errorf("neither the main model (%s) nor the sidecar (%s) has vision capability; bind one with /setmodel vision <number|name>", r.cfg.Model, r.cfg.SidecarModel)
}

func (r *visionRouter) describeWithRole(ctx context.Context, in visionInput, question string) (string, error) {
	if in.path != "" {
		return r.visionRole.Describe(ctx, in.path, question)
	}
	return r.visionRole.DescribePayload(ctx, in.payload, in.label, question)
}

func (r *visionRouter) describeWithSidecar(ctx context.Context, in visionInput, question string) (string, error) {
	if in.path != "" {
		return r.pool.DescribeImage(ctx, in.path, question)
	}
	return r.pool.DescribePayload(ctx, in.payload, in.label, question)
}

// sidecarMaySee reports whether the sidecar route is worth trying: enabled
// and known-capable, or enabled with unknown capability (honest attempt).
func (r *visionRouter) sidecarMaySee() bool {
	if r == nil || r.cfg == nil || r.pool == nil || !r.pool.Enabled() {
		return false
	}
	return r.cfg.SidecarVisionAvailable || !r.cfg.SidecarVisionKnown
}

func (r *visionRouter) mainMaySee() bool {
	if r == nil || r.cfg == nil || r.client == nil {
		return false
	}
	return r.cfg.VisionAvailable || !r.cfg.VisionKnown
}

// describeWithMain is the fallback when the sidecar cannot see: the main
// model looks itself. Timeouts mirror the sidecar call budget.
func (r *visionRouter) describeWithMain(ctx context.Context, in visionInput, question string) (string, error) {
	payload := in.payload
	if in.path != "" {
		encoded, err := vision.EncodeFile(in.path)
		if err != nil {
			return "", err
		}
		payload = encoded
	}
	callCtx, cancel := context.WithTimeout(ctx, sidecar.CallTimeout)
	defer cancel()
	resp, err := r.client.ChatSync(callCtx, ollama.ChatRequest{
		Model: r.cfg.Model,
		Messages: []ollama.Message{
			{Role: "system", Content: sidecar.VisualSystemFor(question)},
			{Role: "user", Content: fmt.Sprintf("%s\n\n[image: %s]", question, in.label), Images: []string{payload}},
		},
		// Thinking must be off, not merely omitted: Ollama enables it by default
		// on capable models. OpenAI-compatible servers ignore this flag, so the
		// budget is generous and ExtractAnswer recovers the observations from
		// reasoning output when Content comes back empty.
		Think:     ollama.ThinkOff(),
		KeepAlive: r.cfg.KeepAliveFor(config.RoleMain),
		Options:   ollama.ChatOptions{Temperature: 0, NumPredict: sidecar.DescribeNumPredict},
	})
	if err != nil {
		return "", err
	}
	answer := sidecar.ExtractAnswer(resp)
	if answer == "" {
		return "", fmt.Errorf("model returned empty description")
	}
	return answer, nil
}
