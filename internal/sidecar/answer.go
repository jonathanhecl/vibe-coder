package sidecar

import (
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// DescribeNumPredict is the token budget for vision descriptions. It is
// deliberately generous: OpenAI-compatible servers (llama-server, vLLM,
// LM Studio) ignore Ollama's think=false, so a reasoning model may spend
// hundreds of tokens thinking before it writes the answer. A small budget
// ends the reply with finish_reason=length and an empty content field.
const DescribeNumPredict = 2048

// ExtractAnswer returns the usable answer text from a chat response.
// It strips inline <think>/<thinking> blocks from Content and, when
// Content is empty (the budget ran out mid-reasoning, or the server put
// everything in reasoning_content), falls back to the reasoning text so
// the caller still gets the model's visual observations instead of an
// "empty description" error.
func ExtractAnswer(resp ollama.ChatResponse) string {
	if answer := strings.TrimSpace(stripThinkBlocks(resp.Content)); answer != "" {
		return answer
	}
	return strings.TrimSpace(stripThinkTags(resp.Thinking))
}

// stripThinkBlocks removes complete <think>...</think> (or <thinking>)
// blocks. An unclosed opening tag drops everything after it, because that
// text is reasoning that never reached a final answer.
func stripThinkBlocks(s string) string {
	for _, tag := range []string{"think", "thinking"} {
		open, closeTag := "<"+tag+">", "</"+tag+">"
		for {
			i := strings.Index(s, open)
			if i < 0 {
				break
			}
			rest := s[i+len(open):]
			j := strings.Index(rest, closeTag)
			if j < 0 {
				s = s[:i]
				break
			}
			s = s[:i] + rest[j+len(closeTag):]
		}
	}
	return s
}

func stripThinkTags(s string) string {
	for _, tag := range []string{"<think>", "</think>", "<thinking>", "</thinking>"} {
		s = strings.ReplaceAll(s, tag, "")
	}
	return s
}
