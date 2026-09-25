package jevstyle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

const (
	// MinOptions is the minimum number of choices for a decision.
	MinOptions = 2
	// MaxOptions is the maximum number of choices supported (A-Z).
	MaxOptions = 26
	// DefaultTimeout is the per-call timeout when querying the decision model.
	DefaultTimeout = 30 * time.Second
)

// DecisionRequest holds the inputs for a JEV Style decision function turn.
// JEV Style models are strictly text-only decision functions: they operate
// exclusively on text state, question, and candidate options (no images,
// no audio, no function-calling schemas).
type DecisionRequest struct {
	State    string
	Question string
	Options  []string
}

// DecisionResponse contains the parsed result of a decision function turn.
// The model only returns a single choice letter (A-Z).
type DecisionResponse struct {
	Choice string // Selected letter: "A", "B", ...
	Index  int    // 0-based index corresponding to Choice
	Option string // Text of the selected option
	Raw    string // Raw content returned by the model
}

// Decider defines the interface for executing discrete, text-only decision choices.
type Decider interface {
	Decide(ctx context.Context, req DecisionRequest) (*DecisionResponse, error)
	DecideBool(ctx context.Context, state, question string) (bool, error)
	DecideChoice(ctx context.Context, state, question string, options ...string) (string, int, error)
	Enabled() bool
	Model() string
}

// Client coordinates requests to the JEV Style decision model via Ollama.
type Client struct {
	cfg     *config.Config
	client  ollama.Client
	sem     chan struct{}
	timeout time.Duration
}

// Option configures Client behavior.
type Option func(*Client)

// WithMaxParallel limits concurrent decision requests.
func WithMaxParallel(n int) Option {
	return func(c *Client) {
		if n < 1 {
			n = 1
		}
		c.sem = make(chan struct{}, n)
	}
}

// WithTimeout overrides the default decision request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// New creates a JEV Style decision client.
func New(cfg *config.Config, client ollama.Client, opts ...Option) *Client {
	c := &Client{
		cfg:     cfg,
		client:  client,
		sem:     make(chan struct{}, 2),
		timeout: DefaultTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Enabled reports whether JEV Style is configured and ready.
func (c *Client) Enabled() bool {
	return c != nil && c.cfg != nil && c.cfg.JevstyleInUse() && c.client != nil
}

// Model returns the configured JEV Style model name.
func (c *Client) Model() string {
	if c == nil || c.cfg == nil {
		return ""
	}
	return strings.TrimSpace(c.cfg.JevstyleModel)
}

// Decide executes a structured decision prompt and parses the model's option choice.
func (c *Client) Decide(ctx context.Context, req DecisionRequest) (*DecisionResponse, error) {
	if !c.Enabled() {
		return nil, errors.New("jevstyle decision model is not configured")
	}

	prompt, err := FormatPrompt(req.State, req.Question, req.Options)
	if err != nil {
		return nil, fmt.Errorf("format jevstyle prompt: %w", err)
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

	resp, err := c.client.ChatSync(callCtx, ollama.ChatRequest{
		Model: c.Model(),
		Messages: []ollama.Message{
			{Role: "user", Content: prompt},
		},
		Options: ollama.ChatOptions{
			Temperature: 0,
			NumPredict:  10,
		},
		Stream: false,
	})
	if err != nil {
		return nil, fmt.Errorf("call jevstyle model: %w", err)
	}

	letter, idx, err := ParseChoice(resp.Content, len(req.Options))
	if err != nil {
		return nil, fmt.Errorf("parse decision: %w (raw: %q)", err, resp.Content)
	}

	return &DecisionResponse{
		Choice: letter,
		Index:  idx,
		Option: req.Options[idx],
		Raw:    resp.Content,
	}, nil
}

// DecideBool executes a binary decision with options "Yes" (true) and "No" (false).
func (c *Client) DecideBool(ctx context.Context, state, question string) (bool, error) {
	resp, err := c.Decide(ctx, DecisionRequest{
		State:    state,
		Question: question,
		Options:  []string{"Yes", "No"},
	})
	if err != nil {
		return false, err
	}
	return resp.Index == 0, nil
}

// DecideChoice executes a decision with the provided list of options.
func (c *Client) DecideChoice(ctx context.Context, state, question string, options ...string) (string, int, error) {
	resp, err := c.Decide(ctx, DecisionRequest{
		State:    state,
		Question: question,
		Options:  options,
	})
	if err != nil {
		return "", -1, err
	}
	return resp.Option, resp.Index, nil
}

// FormatPrompt constructs the exact prompt template expected by JEV Style models.
func FormatPrompt(state, question string, options []string) (string, error) {
	if len(options) < MinOptions {
		return "", fmt.Errorf("at least %d options required, got %d", MinOptions, len(options))
	}
	if len(options) > MaxOptions {
		return "", fmt.Errorf("at most %d options supported, got %d", MaxOptions, len(options))
	}

	var b strings.Builder
	b.WriteString("You are a decision function. Read the state, then answer the question by choosing exactly one option.\n\n")
	b.WriteString("[State]\n")
	b.WriteString(strings.TrimSpace(state))
	b.WriteString("\n\n[Question]\n")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\n[Options]\n")

	for i, opt := range options {
		letter := rune('A' + i)
		b.WriteRune(letter)
		b.WriteString(". ")
		b.WriteString(strings.TrimSpace(opt))
		b.WriteByte('\n')
	}

	b.WriteString("\nAnswer:")
	return b.String(), nil
}

// ParseChoice extracts the option letter (A-Z) from the model's output.
func ParseChoice(raw string, numOptions int) (string, int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", -1, errors.New("empty response from decision model")
	}

	for _, r := range trimmed {
		if unicode.IsLetter(r) {
			upper := unicode.ToUpper(r)
			if upper < 'A' || upper > 'Z' {
				return "", -1, fmt.Errorf("invalid choice letter %c: must be in A-Z", upper)
			}
			idx := int(upper - 'A')
			if idx >= numOptions {
				return "", -1, fmt.Errorf("choice %c out of range for %d options", upper, numOptions)
			}
			return string(upper), idx, nil
		}
	}

	return "", -1, fmt.Errorf("could not extract option letter from %q", raw)
}
