package config

import "github.com/jonathanhecl/vibe-coder/internal/ollama"

// CapabilityVerdict is the outcome of checking a model against a role.
type CapabilityVerdict string

const (
	// CapabilityOK means the provider reported the required capability.
	CapabilityOK CapabilityVerdict = "ok"
	// CapabilityUnverified means no capability data was available, so the
	// user's choice is trusted.
	CapabilityUnverified CapabilityVerdict = "unverified"
	// CapabilityUnsupported means the provider reported capabilities and the
	// required one is missing.
	CapabilityUnsupported CapabilityVerdict = "unsupported"
	// CapabilityNotRequired means the role has no capability requirement.
	CapabilityNotRequired CapabilityVerdict = "not-required"
)

// CapabilityStatus describes whether a model can serve a role.
type CapabilityStatus struct {
	// Role is the role being checked.
	Role Role
	// Required is the capability the role needs; empty when none is required.
	Required string
	// Verdict is the check outcome.
	Verdict CapabilityVerdict
	// Available reports whether the model may be used for the role. It is true
	// for every verdict except CapabilityUnsupported.
	Available bool
	// Verified reports whether the verdict came from provider metadata rather
	// than from trusting the user.
	Verified bool
}

// CheckModelForRole evaluates a model against the capability a role requires.
//
// Capabilities are authoritative only for native Ollama providers that report
// them. Other providers, and Ollama deployments that report nothing, leave the
// capability unverified: the user is trusted and the model is accepted.
func CheckModelForRole(kind ProviderKind, role Role, model ollama.Model) CapabilityStatus {
	required := CapabilityRequirement(role)
	status := CapabilityStatus{Role: role, Required: required}
	if required == "" {
		status.Verdict = CapabilityNotRequired
		status.Available = true
		return status
	}
	if kind != ProviderOllama || !model.CapabilitiesKnown {
		status.Verdict = CapabilityUnverified
		status.Available = true
		return status
	}
	status.Verified = true
	if modelSupportsCapability(model, required) {
		status.Verdict = CapabilityOK
		status.Available = true
		return status
	}
	status.Verdict = CapabilityUnsupported
	return status
}

// modelSupportsCapability maps a capability name onto the model helper that
// checks it.
func modelSupportsCapability(model ollama.Model, capability string) bool {
	switch capability {
	case CapabilityCompletion:
		return model.SupportsCompletion()
	case CapabilityTools:
		return model.SupportsTools()
	case CapabilityVision:
		return model.SupportsVision()
	case CapabilityThinking:
		return model.SupportsThinking()
	case CapabilityEmbedding:
		return model.SupportsEmbedding()
	case CapabilityDecision:
		return model.SupportsDecision()
	default:
		return model.Supports(capability)
	}
}
