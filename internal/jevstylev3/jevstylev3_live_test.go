//go:build live

package jevstylev3

import (
	"context"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/jevstyle"
)

// TestLiveIntegration runs integration tests against a real JEV v3 server.
// Run with: go test -tags live ./internal/jevstylev3/
// Requires a running server at http://192.168.0.33:8765
func TestLiveIntegration(t *testing.T) {
	endpoint := "http://192.168.0.33:8765"
	client := New(endpoint)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Health check
	if err := client.HealthCheck(ctx); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	t.Log("Health check: OK")

	// DecideBool (noul)
	isRaining, err := client.DecideBool(ctx,
		"The sky is clear and the sun is shining brightly.",
		"Is it currently raining?")
	if err != nil {
		t.Fatalf("DecideBool failed: %v", err)
	}
	if isRaining {
		t.Fatal("expected false for 'is it raining?' with clear sky")
	}
	t.Logf("DecideBool: %v (expected false)", isRaining)

	// DecideChoice
	intent, idx, err := client.DecideChoice(ctx,
		"Hi, I was charged twice for my subscription this month. I want a refund.",
		"Which intent is this?",
		"billing_question", "cancel_subscription", "technical_issue")
	if err != nil {
		t.Fatalf("DecideChoice failed: %v", err)
	}
	if intent != "cancel_subscription" {
		t.Fatalf("expected cancel_subscription, got %q", intent)
	}
	t.Logf("DecideChoice: %s (index %d)", intent, idx)

	// Decide with score (urgency)
	resp, err := client.Decide(ctx, jevstyle.DecisionRequest{
		State:    "Ticket: the checkout page returns HTTP 500 for every customer since the last deploy 2 hours ago.",
		Question: "How urgent is this ticket?",
		Options:  []string{"not urgent", "normal", "urgent", "critical"},
	})
	if err != nil {
		t.Fatalf("Decide (score) failed: %v", err)
	}
	if resp.Score < 2.0 {
		t.Fatalf("expected urgency score >= 2.0, got %f", resp.Score)
	}
	t.Logf("Decide (score): %s (score=%.2f, confidence=%.2f)", resp.Option, resp.Score, resp.Confidence)

	// IsCommandDangerous
	dangerous, err := client.IsCommandDangerous(ctx, "rm -rf /")
	if err != nil {
		t.Fatalf("IsCommandDangerous failed: %v", err)
	}
	if !dangerous {
		t.Fatal("expected dangerous=true for 'rm -rf /'")
	}
	t.Logf("IsCommandDangerous('rm -rf /'): %v", dangerous)

	safe, err := client.IsCommandDangerous(ctx, "ls")
	if err != nil {
		t.Fatalf("IsCommandDangerous failed: %v", err)
	}
	if safe {
		t.Fatal("expected dangerous=false for 'ls'")
	}
	t.Logf("IsCommandDangerous('ls'): %v", safe)

	// DisambiguatePath
	chosen, ok, err := client.DisambiguatePath(ctx, "config file", []string{
		"internal/config/config.go",
		"internal/agent/agent.go",
		"internal/slash/jevstyle.go",
	})
	if err != nil {
		t.Fatalf("DisambiguatePath failed: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if chosen != "internal/config/config.go" {
		t.Fatalf("expected internal/config/config.go, got %q", chosen)
	}
	t.Logf("DisambiguatePath: %s", chosen)

	// ClassifyFailure
	category, err := client.ClassifyFailure(ctx, "syntax error on line 12: unexpected token")
	if err != nil {
		t.Fatalf("ClassifyFailure failed: %v", err)
	}
	if category == "" || category == " (uncertain)" {
		t.Fatalf("unexpected category: %q", category)
	}
	t.Logf("ClassifyFailure: %s", category)

	// ClassifyCommit
	commitType, err := client.ClassifyCommit(ctx, "+func TestNewFeature(t *testing.T) { ... }")
	if err != nil {
		t.Fatalf("ClassifyCommit failed: %v", err)
	}
	validTypes := map[string]bool{"feat": true, "fix": true, "refactor": true, "test": true, "docs": true, "chore": true}
	if !validTypes[commitType] {
		t.Fatalf("expected valid commit type, got %q", commitType)
	}
	t.Logf("ClassifyCommit: %s", commitType)

	// AskSafety
	safetyDecision, err := client.AskSafety(ctx, "bash", "rm -rf /var/www/production/*")
	if err != nil {
		t.Fatalf("AskSafety failed: %v", err)
	}
	if !safetyDecision.Blocked() {
		t.Fatal("expected block for 'rm -rf /var/www/production/*'")
	}
	t.Logf("AskSafety: action=%s risk=%.2f confidence=%.2f",
		safetyDecision.Action, safetyDecision.RiskScore, safetyDecision.Confidence)

	safetyDecision2, err := client.AskSafety(ctx, "bash", "ls -la")
	if err != nil {
		t.Fatalf("AskSafety failed: %v", err)
	}
	if !safetyDecision2.Allowed() {
		t.Fatal("expected allow for 'ls -la'")
	}
	t.Logf("AskSafety: action=%s risk=%.2f confidence=%.2f",
		safetyDecision2.Action, safetyDecision2.RiskScore, safetyDecision2.Confidence)

	t.Log("All live integration tests passed!")
}
