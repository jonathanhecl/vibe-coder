package tools

import "context"

type Tool interface {
	Name() string
	Description() string
	Schema() Schema
	Execute(ctx context.Context, params map[string]any) Result
}

type Result struct {
	CallID  string
	Output  string
	IsError bool
}

type Schema struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
