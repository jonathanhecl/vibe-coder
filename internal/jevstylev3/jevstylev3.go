// Package jevstylev3 provides a client for JEV Style v3 (jev-style serve),
// which exposes a native JSON structured API at POST /v1/systemone.
//
// Unlike JEV Style v1/v2 (which operates as an Ollama model returning a
// letter A-Z), v3 accepts typed questions (noul, choice, score) and
// returns structured answers with probabilities and confidence.
//
// This package implements jevstyle.Decider so it can be used as a
// drop-in replacement for jevstyle.Client.
package jevstylev3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/jevstyle"
)

const (
	// DefaultTimeout is the per-call timeout for v3 requests.
	DefaultTimeout = 30 * time.Second

	// decisionQuestionID is the single question id used for all decisions.
	decisionQuestionID = "decision"

	// dangerousThreshold is the score threshold (0-indexed) for IsCommandDangerous
	// and IsActionDangerous. Scores >= this value are considered dangerous.
	// Scale: ["safe", "low risk", "medium risk", "high risk", "critical"]
	dangerousThreshold = 2.0

	// goalCompleteThreshold is the score threshold for CheckGoalCompletion.
	// Scale: ["not started", "in progress", "mostly done", "completed"]
	goalCompleteThreshold = 2.0

	// minConfidence is the minimum confidence for classification results.
	// Below this, the result is marked as "(uncertain)".
	minConfidence = 0.5
)

// Client is a JEV Style v3 HTTP client that implements jevstyle.Decider.
type Client struct {
	endpoint string
	client   *http.Client
	timeout  time.Duration
}

// New creates a new JEV Style v3 client for the given endpoint.
func New(endpoint string) *Client {
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   &http.Client{},
		timeout:  DefaultTimeout,
	}
}

// WithTimeout overrides the default request timeout.
func (c *Client) WithTimeout(d time.Duration) *Client {
	if d > 0 {
		c.timeout = d
	}
	return c
}

// Enabled reports whether the client is configured.
func (c *Client) Enabled() bool {
	return c != nil && c.endpoint != ""
}

// Model returns the endpoint identifier.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// HealthCheck verifies the server is responding.
func (c *Client) HealthCheck(ctx context.Context) error {
	if !c.Enabled() {
		return errors.New("jevstylev3: endpoint not configured")
	}

	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet,
		c.endpoint+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("jevstylev3: build health check request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("jevstylev3: health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("jevstylev3: health check returned status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var health struct {
		Status string `json:"status"`
		Model  string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return fmt.Errorf("jevstylev3: decode health response: %w", err)
	}
	if health.Status != "ok" {
		return fmt.Errorf("jevstylev3: server not ready (status=%q, model=%q)",
			health.Status, health.Model)
	}
	return nil
}

// v3Request is the JSON body sent to POST /v1/systemone.
type v3Request struct {
	State     string                `json:"state"`
	Questions map[string]v3Question `json:"questions"`
}

type v3Question struct {
	Type         string      `json:"type"`
	Instructions string      `json:"instructions"`
	Criteria     interface{} `json:"criteria,omitempty"`
}

// v3Response is the JSON response from POST /v1/systemone.
type v3Response struct {
	Model     string              `json:"model"`
	Answers   map[string]v3Answer `json:"answers"`
	Usage     v3Usage             `json:"usage"`
	LatencyMs float64             `json:"latency_ms"`
}

type v3Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]string  `json:"legend"`
}

type v3Usage struct {
	InputTokens  int `json:"input_tokens"`
	StateTokens  int `json:"state_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// callV3 sends a typed question to the v3 server and returns the answer.
func (c *Client) callV3(ctx context.Context, qType, state, question string, criteria interface{}) (*v3Answer, error) {
	if !c.Enabled() {
		return nil, errors.New("jevstylev3: endpoint not configured")
	}

	payload := v3Request{
		State: state,
		Questions: map[string]v3Question{
			decisionQuestionID: {
				Type:         qType,
				Instructions: question,
				Criteria:     criteria,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("jevstylev3: marshal request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost,
		c.endpoint+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("jevstylev3: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jevstylev3: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("jevstylev3: server returned status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result v3Response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("jevstylev3: decode response: %w", err)
	}

	answer, ok := result.Answers[decisionQuestionID]
	if !ok {
		return nil, errors.New("jevstylev3: response missing 'decision' answer")
	}
	return &answer, nil
}

// callNoul sends a yes/no question and returns P(true).
func (c *Client) callNoul(ctx context.Context, state, question string) (float64, error) {
	ans, err := c.callV3(ctx, "noul", state, question, nil)
	if err != nil {
		return 0, err
	}
	return ans.Noul, nil
}

// callChoice sends a multiple-choice question and returns the answer.
func (c *Client) callChoice(ctx context.Context, state, question string, options []string) (*v3Answer, error) {
	criteria := make(map[string]interface{}, len(options))
	for _, opt := range options {
		criteria[opt] = nil
	}
	return c.callV3(ctx, "choice", state, question, criteria)
}

// callScore sends an ordinal scale question and returns the answer.
func (c *Client) callScore(ctx context.Context, state, question string, options []string) (*v3Answer, error) {
	return c.callV3(ctx, "score", state, question, options)
}

// Decide executes a decision with the provided options.
// Uses choice type for categorical decisions, score type for ordinal scales.
func (c *Client) Decide(ctx context.Context, req jevstyle.DecisionRequest) (*jevstyle.DecisionResponse, error) {
	if len(req.Options) < 2 {
		return nil, errors.New("jevstylev3: at least 2 options required")
	}

	// Use score type for ordinal scales (danger, completion, etc.)
	if isOrdinalScale(req.Options) {
		ans, err := c.callScore(ctx, req.State, req.Question, req.Options)
		if err != nil {
			return nil, err
		}

		scoreIdx := int(ans.Score + 0.5)
		if scoreIdx < 0 {
			scoreIdx = 0
		}
		if scoreIdx >= len(req.Options) {
			scoreIdx = len(req.Options) - 1
		}

		probabilities := make(map[string]float64, len(req.Options))
		for i, opt := range req.Options {
			if p, ok := ans.Probabilities[fmt.Sprintf("%d", i)]; ok {
				probabilities[opt] = p
			} else {
				probabilities[opt] = 0
			}
		}

		return &jevstyle.DecisionResponse{
			Choice: req.Options[scoreIdx],
			Index:  scoreIdx,
			Option: req.Options[scoreIdx],
			Score:  ans.Score,
			Raw:    fmt.Sprintf("score=%.2f confidence=%.2f", ans.Score, ans.Confidence),
		}, nil
	}

	// Use choice type for categorical decisions
	ans, err := c.callChoice(ctx, req.State, req.Question, req.Options)
	if err != nil {
		return nil, err
	}

	idx := -1
	for i, opt := range req.Options {
		if opt == ans.Choice {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("jevstylev3: choice %q not in options", ans.Choice)
	}

	return &jevstyle.DecisionResponse{
		Choice: ans.Choice,
		Index:  idx,
		Option: ans.Choice,
		Raw:    fmt.Sprintf("choice=%s confidence=%.2f", ans.Choice, ans.Confidence),
	}, nil
}

// isOrdinalScale detects if the options look like an ordinal scale.
func isOrdinalScale(options []string) bool {
	if len(options) < 2 || len(options) > 10 {
		return false
	}

	knownScales := [][]string{
		{"safe", "low risk", "medium risk", "high risk", "critical"},
		{"not started", "in progress", "mostly done", "completed"},
		{"low", "medium", "high"},
		{"low", "medium", "high", "critical"},
		{"not urgent", "normal", "urgent", "critical"},
		{"poor", "fair", "good", "excellent"},
		{"strongly disagree", "disagree", "neutral", "agree", "strongly agree"},
	}

	for _, scale := range knownScales {
		if len(options) != len(scale) {
			continue
		}
		match := true
		for i, opt := range options {
			if strings.ToLower(strings.TrimSpace(opt)) != scale[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}

	return false
}

// DecideBool executes a binary decision using noul (P(true) >= 0.5).
func (c *Client) DecideBool(ctx context.Context, state, question string) (bool, error) {
	p, err := c.callNoul(ctx, state, question)
	if err != nil {
		return false, err
	}
	return p >= 0.5, nil
}

// DecideChoice executes a choice decision and returns the selected option.
func (c *Client) DecideChoice(ctx context.Context, state, question string, options ...string) (string, int, error) {
	resp, err := c.Decide(ctx, jevstyle.DecisionRequest{
		State:    state,
		Question: question,
		Options:  options,
	})
	if err != nil {
		return "", -1, err
	}
	return resp.Option, resp.Index, nil
}

// dangerScale is the ordinal scale used for danger assessment.
var dangerScale = []string{"safe", "low risk", "medium risk", "high risk", "critical"}

// IsCommandDangerous evaluates whether a shell command is potentially risky.
// Uses a score scale: returns true if the danger level is >= "medium risk".
func (c *Client) IsCommandDangerous(ctx context.Context, command string) (bool, error) {
	state := fmt.Sprintf("The agent proposes to run the following shell command:\n`%s`",
		strings.TrimSpace(command))
	question := "How dangerous is this command to the system or repository?"

	ans, err := c.callScore(ctx, state, question, dangerScale)
	if err != nil {
		return false, err
	}
	return ans.Score >= dangerousThreshold, nil
}

// IsActionDangerous evaluates whether a non-shell tool action is risky.
// Uses a score scale: returns true if the danger level is >= "medium risk".
func (c *Client) IsActionDangerous(ctx context.Context, toolName, summary string) (bool, error) {
	state := fmt.Sprintf("The agent proposes to use the `%s` tool with these arguments:\n%s",
		strings.TrimSpace(toolName), strings.TrimSpace(summary))
	question := "How dangerous is this action to the system, repository, or the user's privacy?"

	ans, err := c.callScore(ctx, state, question, dangerScale)
	if err != nil {
		return false, err
	}
	return ans.Score >= dangerousThreshold, nil
}

// DisambiguatePath chooses the best matching path among candidates.
func (c *Client) DisambiguatePath(ctx context.Context, hint string, candidates []string) (string, bool, error) {
	if len(candidates) == 0 {
		return "", false, nil
	}
	if len(candidates) == 1 {
		return candidates[0], true, nil
	}

	state := fmt.Sprintf("Context and reference:\n%s", strings.TrimSpace(hint))
	question := "Which candidate path best matches this reference or intent?"

	resp, err := c.Decide(ctx, jevstyle.DecisionRequest{
		State:    state,
		Question: question,
		Options:  candidates,
	})
	if err != nil {
		return "", false, err
	}
	return resp.Option, true, nil
}

// goalScale is the ordinal scale used for goal completion assessment.
var goalScale = []string{"not started", "in progress", "mostly done", "completed"}

// CheckGoalCompletion evaluates whether a goal has been fully achieved.
// Uses a score scale: returns true if completion is >= "mostly done".
func (c *Client) CheckGoalCompletion(ctx context.Context, goal, recentProgress string) (bool, error) {
	state := fmt.Sprintf("User goal:\n%s\n\nRecent execution progress and results:\n%s",
		strings.TrimSpace(goal), strings.TrimSpace(recentProgress))
	question := "Has the user goal been fully accomplished and completed?"

	ans, err := c.callScore(ctx, state, question, goalScale)
	if err != nil {
		return false, err
	}
	return ans.Score >= goalCompleteThreshold, nil
}

// ClassifyFailure categorizes an error log into actionable classes.
// Returns the category, with "(uncertain)" appended if confidence is low.
func (c *Client) ClassifyFailure(ctx context.Context, errorLog string) (string, error) {
	options := []string{
		"Syntax or compile error",
		"Missing package, module, or dependency",
		"Failing test assertion or business logic failure",
		"Permission denied or access error",
		"Network error, connection failure, or timeout",
		"Other or unknown cause",
	}

	resp, err := c.Decide(ctx, jevstyle.DecisionRequest{
		State:    fmt.Sprintf("Error or failure log:\n%s", strings.TrimSpace(errorLog)),
		Question: "What is the primary cause of this failure?",
		Options:  options,
	})
	if err != nil {
		return "", err
	}

	if resp.Confidence < minConfidence {
		return resp.Option + " (uncertain)", nil
	}
	return resp.Option, nil
}

// ClassifyCommit suggests the primary conventional commit type for a diff.
// Returns the commit type, with "(uncertain)" appended if confidence is low.
func (c *Client) ClassifyCommit(ctx context.Context, diffSummary string) (string, error) {
	options := []string{
		"fix (bug fix or error resolution)",
		"feat (new feature or capability)",
		"refactor (code reorganization without feature change)",
		"test (adding or updating test cases)",
		"docs (documentation only)",
		"chore (build scripts, dependencies, or maintenance)",
	}

	resp, err := c.Decide(ctx, jevstyle.DecisionRequest{
		State:    fmt.Sprintf("Git diff summary:\n%s", strings.TrimSpace(diffSummary)),
		Question: "What is the primary conventional commit type for these changes?",
		Options:  options,
	})
	if err != nil {
		return "", err
	}

	word := strings.Fields(resp.Option)[0]
	if resp.Confidence < minConfidence {
		return word + " (uncertain)", nil
	}
	return word, nil
}
