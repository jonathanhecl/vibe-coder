// Package decision provides a client for the System One decision protocol:
// POST <endpoint>/v1/systemone with typed questions (noul, choice, score).
// It works against a native Ollama endpoint (>= 0.35, models advertising the
// "decision" capability) or a dedicated System One server such as
// "jev-style serve".
package decision

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
)

const (
	// MinOptions is the minimum number of choices for a decision.
	MinOptions = 2
	// MaxOptions is the maximum number of options offered to the model.
	MaxOptions = 26
	// DefaultTimeout is the per-call timeout for decision requests.
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

	// autoApproveConfidence is the confidence threshold for auto-approving or
	// auto-blocking actions. Below this, the user is asked to decide.
	autoApproveConfidence = 0.70
)

// Request holds the inputs for one decision: a factual state, the question,
// and the candidate options the model must choose exactly one of.
type Request struct {
	State    string
	Question string
	Options  []string
}

// Response is the parsed result of one decision.
type Response struct {
	Choice        string             // Selected option text
	Index         int                // 0-based index into Request.Options
	Option        string             // Text of the selected option
	Raw           string             // Raw answer description
	Score         float64            // 0.0 to len(Options)-1, score-type decisions
	Confidence    float64            // 0.0-1.0
	Probabilities map[string]float64 // Option -> probability
}

// Decider executes discrete, text-only decision choices.
type Decider interface {
	Decide(ctx context.Context, req Request) (*Response, error)
	DecideBool(ctx context.Context, state, question string) (bool, error)
	DecideChoice(ctx context.Context, state, question string, options ...string) (string, int, error)
	IsCommandDangerous(ctx context.Context, command string) (bool, error)
	IsCommandNetworkAccess(ctx context.Context, command string) (bool, error)
	DisambiguatePath(ctx context.Context, hint string, candidates []string) (string, bool, error)
	CheckGoalCompletion(ctx context.Context, goal, recentProgress string) (bool, error)
	ClassifyFailure(ctx context.Context, errorLog string) (string, error)
	ClassifyCommit(ctx context.Context, diffSummary string) (string, error)
	CheckThinkingLoop(ctx context.Context, thinkingText string) (bool, error)
	Enabled() bool
	Model() string
}

// SafetyDecision represents the outcome of an AskSafety query.
type SafetyDecision struct {
	Action     string  // "allow", "review", or "block"
	RiskScore  float64 // ordinal risk level
	Confidence float64 // 0.0-1.0 model confidence
}

// Allowed reports whether the decision is to allow the action.
func (d SafetyDecision) Allowed() bool { return d.Action == "allow" }

// Blocked reports whether the decision is to block the action.
func (d SafetyDecision) Blocked() bool { return d.Action == "block" }

// NeedsReview reports whether the decision is uncertain and needs user input.
func (d SafetyDecision) NeedsReview() bool { return d.Action == "review" }

// Confident reports whether the model confidence is high enough to auto-decide.
func (d SafetyDecision) Confident() bool { return d.Confidence >= autoApproveConfidence }

// Info describes the backend a Check call probed.
type Info struct {
	Backend string // "ollama" or "systemone-server"
	Model   string
	Vision  bool
}

// Client is a System One protocol HTTP client implementing Decider.
type Client struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
	timeout  time.Duration
	sem      chan struct{}

	// serverModel is learned from the backend during Check; it backs Model()
	// when no explicit model is configured.
	serverModel string
}

// Option configures Client behavior.
type Option func(*Client)

// WithAPIKey sends Authorization: Bearer <key> on every request.
func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = strings.TrimSpace(key) }
}

// WithTimeout overrides the default request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithHTTPClient replaces the underlying http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.client = h
		}
	}
}

// New creates a System One decision client. The endpoint is a base URL
// (trailing "/", "/v1", or "/v1/systemone" are normalized away); model may be
// empty for dedicated servers that pin their own model.
func New(endpoint, model string, opts ...Option) *Client {
	c := &Client{
		endpoint: normalizeEndpoint(endpoint),
		model:    strings.TrimSpace(model),
		client:   &http.Client{},
		timeout:  DefaultTimeout,
		sem:      make(chan struct{}, 2),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// normalizeEndpoint reduces an endpoint to its base URL so every caller can
// pass the bare host, a "/v1" root, or a full "/v1/systemone" URL.
func normalizeEndpoint(raw string) string {
	e := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/v1/systemone", "/v1"} {
		if strings.HasSuffix(e, suffix) {
			e = strings.TrimSuffix(e, suffix)
		}
	}
	return strings.TrimRight(e, "/")
}

// Enabled reports whether the client is configured.
func (c *Client) Enabled() bool {
	return c != nil && c.endpoint != ""
}

// Model returns the configured decision model, the model learned from the
// backend during Check, or the endpoint as a last resort.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	if c.model != "" {
		return c.model
	}
	if c.serverModel != "" {
		return c.serverModel
	}
	return c.endpoint
}

// Endpoint returns the normalized base URL.
func (c *Client) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// Check probes the endpoint and reports which backend answers:
// a dedicated System One server (GET /healthz returns {"status":"ok",...})
// or native Ollama (no /healthz; /api/show must advertise the "decision"
// capability for the configured model). On Ollama a model name is required.
func (c *Client) Check(ctx context.Context) (Info, error) {
	if !c.Enabled() {
		return Info{}, errors.New("decision: endpoint not configured")
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, c.endpoint+"/healthz", nil)
	if err != nil {
		return Info{}, fmt.Errorf("decision: build health check request: %w", err)
	}
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("decision: health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return c.checkOllama(callCtx)
	}
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("decision: health check returned status %d: %s",
			resp.StatusCode, c.readErrorBody(resp))
	}
	var health struct {
		Status string `json:"status"`
		Model  string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return Info{}, fmt.Errorf("decision: decode health response: %w", err)
	}
	if health.Status != "ok" {
		return Info{}, fmt.Errorf("decision: server not ready (status=%q)", health.Status)
	}
	if strings.TrimSpace(health.Model) != "" {
		c.serverModel = strings.TrimSpace(health.Model)
	}
	return Info{
		Backend: "systemone-server",
		Model:   c.Model(),
	}, nil
}

// checkOllama verifies the configured model via POST /api/show on a native
// Ollama endpoint and requires the "decision" capability.
func (c *Client) checkOllama(ctx context.Context) (Info, error) {
	if c.model == "" {
		return Info{}, errors.New("a decision model name is required for an Ollama endpoint (set DECISION_MODEL)")
	}
	body, err := json.Marshal(map[string]string{"model": c.model})
	if err != nil {
		return Info{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/show", bytes.NewReader(body))
	if err != nil {
		return Info{}, fmt.Errorf("decision: build show request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("decision: show request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("decision: /api/show returned status %d: %s",
			resp.StatusCode, c.readErrorBody(resp))
	}
	var show struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		return Info{}, fmt.Errorf("decision: decode show response: %w", err)
	}
	var hasDecision, hasVision bool
	for _, cap := range show.Capabilities {
		switch cap {
		case "decision":
			hasDecision = true
		case "vision":
			hasVision = true
		}
	}
	if !hasDecision {
		return Info{}, fmt.Errorf("model %q does not advertise the 'decision' capability (Ollama >= 0.35 required)", c.model)
	}
	return Info{
		Backend: "ollama",
		Model:   c.model,
		Vision:  hasVision,
	}, nil
}

func (c *Client) authorize(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// readErrorBody extracts a useful error string from a non-200 response,
// covering {"error":"..."} (Ollama), {"error":{"message":...}} (OpenAI-style
// servers), and arbitrary bodies clipped to 512 bytes.
func (c *Client) readErrorBody(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	trimmed := strings.TrimSpace(string(body))
	var parsed struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && len(parsed.Error) > 0 {
		var s string
		if json.Unmarshal(parsed.Error, &s) == nil && s != "" {
			return s
		}
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(parsed.Error, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
	}
	const clip = 512
	if len(trimmed) > clip {
		trimmed = trimmed[:clip] + "…"
	}
	return trimmed
}

// soRequest is the JSON body sent to POST /v1/systemone.
type soRequest struct {
	Model     string              `json:"model,omitempty"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type question struct {
	Type         string      `json:"type"`
	Instructions string      `json:"instructions"`
	Criteria     interface{} `json:"criteria,omitempty"`
}

// soResponse is the JSON response from POST /v1/systemone.
type soResponse struct {
	Model     string              `json:"model"`
	Answers   map[string]soAnswer `json:"answers"`
	Usage     soUsage             `json:"usage"`
	LatencyMs float64             `json:"latency_ms"`
}

type soAnswer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]string  `json:"legend"`
}

type soUsage struct {
	InputTokens  int `json:"input_tokens"`
	StateTokens  int `json:"state_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// call posts one request body to /v1/systemone under the concurrency
// semaphore and per-call timeout, and decodes the answers map.
func (c *Client) call(ctx context.Context, payload soRequest) (map[string]soAnswer, error) {
	if !c.Enabled() {
		return nil, errors.New("decision: endpoint not configured")
	}
	if c.model != "" {
		payload.Model = c.model
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("decision: marshal request: %w", err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost,
		c.endpoint+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decision: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("decision: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("decision: server returned status %d: %s",
			resp.StatusCode, c.readErrorBody(resp))
	}
	var result soResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decision: decode response: %w", err)
	}
	return result.Answers, nil
}

// callOne sends a single typed question and returns its validated answer.
func (c *Client) callOne(ctx context.Context, qType, state, instructions string, criteria interface{}) (*soAnswer, error) {
	answers, err := c.call(ctx, soRequest{
		State: state,
		Questions: map[string]question{
			decisionQuestionID: {
				Type:         qType,
				Instructions: instructions,
				Criteria:     criteria,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	ans, ok := answers[decisionQuestionID]
	if !ok {
		return nil, errors.New("decision: response missing 'decision' answer")
	}
	return &ans, nil
}

// callNoul sends a yes/no question and returns P(true).
func (c *Client) callNoul(ctx context.Context, state, question string) (float64, error) {
	ans, err := c.callOne(ctx, "noul", state, question, nil)
	if err != nil {
		return 0, err
	}
	if ans.Noul < 0 || ans.Noul > 1 {
		return 0, fmt.Errorf("decision: noul %.3f out of range [0,1]", ans.Noul)
	}
	return ans.Noul, nil
}

// callChoice sends a multiple-choice question and returns the answer.
func (c *Client) callChoice(ctx context.Context, state, question string, options []string) (*soAnswer, error) {
	criteria := make(map[string]interface{}, len(options))
	for _, opt := range options {
		criteria[opt] = nil
	}
	ans, err := c.callOne(ctx, "choice", state, question, criteria)
	if err != nil {
		return nil, err
	}
	found := false
	for _, opt := range options {
		if opt == ans.Choice {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("decision: choice %q not in offered options", ans.Choice)
	}
	return ans, nil
}

// callScore sends an ordinal scale question and returns the answer.
func (c *Client) callScore(ctx context.Context, state, question string, options []string) (*soAnswer, error) {
	ans, err := c.callOne(ctx, "score", state, question, options)
	if err != nil {
		return nil, err
	}
	max := float64(len(options)-1) + 1e-6
	if ans.Score < -1e-6 || ans.Score > max {
		return nil, fmt.Errorf("decision: score %.3f out of range [0,%d]", ans.Score, len(options)-1)
	}
	return ans, nil
}

// Decide executes a decision with the provided options.
// Uses choice type for categorical decisions, score type for ordinal scales.
func (c *Client) Decide(ctx context.Context, req Request) (*Response, error) {
	if len(req.Options) < MinOptions {
		return nil, fmt.Errorf("decision: at least %d options required", MinOptions)
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

		return &Response{
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
		return nil, fmt.Errorf("decision: choice %q not in options", ans.Choice)
	}

	return &Response{
		Choice:        ans.Choice,
		Index:         idx,
		Option:        ans.Choice,
		Raw:           fmt.Sprintf("choice=%s confidence=%.2f", ans.Choice, ans.Confidence),
		Confidence:    ans.Confidence,
		Probabilities: ans.Probabilities,
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
	resp, err := c.Decide(ctx, Request{
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

// IsCommandNetworkAccess evaluates whether a shell command attempts external internet access.
func (c *Client) IsCommandNetworkAccess(ctx context.Context, command string) (bool, error) {
	state := fmt.Sprintf("The agent proposes to run the following shell command while in offline mode:\n`%s`",
		strings.TrimSpace(command))
	question := "Does this command attempt to access the external internet, make outbound network requests, or contact remote servers outside of localhost?"
	p, err := c.callNoul(ctx, state, question)
	if err != nil {
		return false, err
	}
	return p >= 0.5, nil
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
	cands := candidates
	if len(cands) > MaxOptions {
		cands = cands[:MaxOptions]
	}
	resp, err := c.Decide(ctx, Request{
		State:    fmt.Sprintf("Context and reference:\n%s", strings.TrimSpace(hint)),
		Question: "Which candidate path best matches this reference or intent?",
		Options:  cands,
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

	resp, err := c.Decide(ctx, Request{
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

	resp, err := c.Decide(ctx, Request{
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

// CheckThinkingLoop evaluates whether recent reasoning paragraphs are stuck in a cyclical loop.
func (c *Client) CheckThinkingLoop(ctx context.Context, thinkingText string) (bool, error) {
	if strings.TrimSpace(thinkingText) == "" {
		return false, nil
	}
	state := fmt.Sprintf("Recent reasoning paragraphs from the model:\n\n%s", strings.TrimSpace(thinkingText))
	question := "Is the model repeating itself or stuck in a cyclical thinking loop without making progress?"
	return c.DecideBool(ctx, state, question)
}

// relevanceScale is the ordinal scale used by ScoreRelevance. Five points give
// enough resolution to separate a strong hit from a weak one while staying
// small enough for the judge to answer several candidates in one batched
// request.
var relevanceScale = []string{
	"irrelevant",
	"weakly relevant",
	"moderately relevant",
	"strongly relevant",
	"perfectly relevant",
}

// relevanceQuestionID names the score question for one candidate inside a
// batched ScoreRelevance request.
func relevanceQuestionID(i int) string { return fmt.Sprintf("relevance%d", i) }

// ScoreRelevance asks the decision model how relevant each candidate is to the
// query and returns one score per candidate, normalized to [0,1]. It prefers a
// single batched request carrying one score question per candidate and falls
// back to one callScore request per candidate when the batched payload cannot
// carry several questions. Empty candidates return an empty slice and a nil
// error; a disabled client returns an error so the caller can fall back.
func (c *Client) ScoreRelevance(ctx context.Context, query string, candidates []string) ([]float64, error) {
	if !c.Enabled() {
		return nil, errors.New("decision: endpoint not configured")
	}
	if len(candidates) == 0 {
		return []float64{}, nil
	}
	scores, err := c.scoreRelevanceBatch(ctx, query, candidates)
	if err == nil {
		return scores, nil
	}
	return c.scoreRelevanceSequential(ctx, query, candidates)
}

// scoreRelevanceBatch carries every candidate in one request: the shared state
// lists the query and the numbered candidates, and each question scores one of
// them on relevanceScale.
func (c *Client) scoreRelevanceBatch(ctx context.Context, query string, candidates []string) ([]float64, error) {
	questions := make(map[string]question, len(candidates))
	for i := range candidates {
		questions[relevanceQuestionID(i)] = question{
			Type: "score",
			Instructions: fmt.Sprintf(
				"How relevant is candidate %d to the query? Judge only whether it helps answer or implement the query.",
				i),
			Criteria: relevanceScale,
		}
	}
	answers, err := c.call(ctx, soRequest{
		State:     relevanceState(query, candidates),
		Questions: questions,
	})
	if err != nil {
		return nil, err
	}
	scores := make([]float64, len(candidates))
	for i := range candidates {
		ans, ok := answers[relevanceQuestionID(i)]
		if !ok {
			return nil, fmt.Errorf("decision: response missing relevance answer for candidate %d", i)
		}
		scores[i] = normalizeRelevance(ans.Score)
	}
	return scores, nil
}

// scoreRelevanceSequential scores one candidate per request, for endpoints that
// cannot carry several questions in one payload. It stops at the first failure
// so a dead endpoint costs one extra call, not one per candidate.
func (c *Client) scoreRelevanceSequential(ctx context.Context, query string, candidates []string) ([]float64, error) {
	scores := make([]float64, len(candidates))
	for i, candidate := range candidates {
		ans, err := c.callScore(ctx,
			relevanceState(query, []string{candidate}),
			"How relevant is this candidate to the query? Judge only whether it helps answer or implement the query.",
			relevanceScale)
		if err != nil {
			return nil, err
		}
		scores[i] = normalizeRelevance(ans.Score)
	}
	return scores, nil
}

// relevanceState renders the shared state for a relevance judgment: the query
// followed by the numbered candidates the questions refer to.
func relevanceState(query string, candidates []string) string {
	var b strings.Builder
	b.WriteString("Query:\n")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n\nCandidates:\n")
	for i, candidate := range candidates {
		fmt.Fprintf(&b, "%d. %s\n", i, strings.TrimSpace(candidate))
	}
	return strings.TrimSpace(b.String())
}

// normalizeRelevance maps an ordinal score on relevanceScale to [0,1].
func normalizeRelevance(score float64) float64 {
	max := float64(len(relevanceScale) - 1)
	if max <= 0 {
		return 0
	}
	n := score / max
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

// riskScale is the ordinal scale used for AskSafety risk assessment.
var riskScale = []string{"none", "low", "medium", "high"}

// safetyActionOptions are the possible actions for AskSafety.
var safetyActionOptions = []string{"allow", "review", "block"}

// AskSafety evaluates a command or action and returns a SafetyDecision with
// action (allow/review/block), risk score, and model confidence.
func (c *Client) AskSafety(ctx context.Context, toolName, detail string) (*SafetyDecision, error) {
	return c.AskSafetyWithContext(ctx, toolName, detail, "")
}

// AskSafetyWithContext is AskSafety plus a runtime context note. The note
// carries facts the model cannot infer from the arguments alone — most
// importantly that a path was supplied by the user (a clipboard paste) rather
// than chosen by the agent. Without it an opaque filename such as
// vibe_clipboard_<random>.png reads as unknown and the decision drops to
// "review", prompting the user for a file they just handed over.
func (c *Client) AskSafetyWithContext(ctx context.Context, toolName, detail, contextNote string) (*SafetyDecision, error) {
	if !c.Enabled() {
		return nil, errors.New("decision: endpoint not configured")
	}

	state := fmt.Sprintf("The agent proposes to use the `%s` tool with these arguments:\n%s",
		strings.TrimSpace(toolName), strings.TrimSpace(detail))
	if note := strings.TrimSpace(contextNote); note != "" {
		state += "\n\nRuntime context (not chosen by the agent):\n" + note
	}

	answers, err := c.call(ctx, soRequest{
		State: state,
		Questions: map[string]question{
			"action": {
				Type:         "choice",
				Instructions: "Should this action be allowed, reviewed by the user, or blocked?",
				Criteria: map[string]interface{}{
					"allow":  "Safe to run automatically",
					"review": "Needs user review before proceeding",
					"block":  "Dangerous, should be blocked",
				},
			},
			"risk": {
				Type:         "score",
				Instructions: "What is the risk level of this action?",
				Criteria:     riskScale,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	actionAns, ok := answers["action"]
	if !ok {
		return nil, errors.New("decision: response missing 'action' answer")
	}
	riskAns, ok := answers["risk"]
	if !ok {
		return nil, errors.New("decision: response missing 'risk' answer")
	}
	validAction := false
	for _, opt := range safetyActionOptions {
		if actionAns.Choice == opt {
			validAction = true
			break
		}
	}
	if !validAction {
		return nil, fmt.Errorf("decision: action %q not in offered options", actionAns.Choice)
	}

	return &SafetyDecision{
		Action:     actionAns.Choice,
		RiskScore:  riskAns.Score,
		Confidence: actionAns.Confidence,
	}, nil
}
