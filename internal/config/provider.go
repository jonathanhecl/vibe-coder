package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ProviderKind identifies the wire protocol a provider speaks.
type ProviderKind string

const (
	// ProviderOllama speaks the native Ollama API (/api/chat, /api/tags, /api/show).
	ProviderOllama ProviderKind = "ollama"
	// ProviderOpenAI speaks the OpenAI-compatible API (/v1/chat/completions, /v1/models).
	ProviderOpenAI ProviderKind = "openai"
)

// DefaultProviderName is the provider that always exists in the registry.
const DefaultProviderName = "ollama"

// Model capability names as reported by provider metadata.
const (
	CapabilityCompletion = "completion"
	CapabilityTools      = "tools"
	CapabilityVision     = "vision"
	CapabilityThinking   = "thinking"
	CapabilityEmbedding  = "embedding"
	CapabilityDecision   = "decision"
)

// Role identifies a model-consuming role.
type Role string

const (
	RoleMain      Role = "main"
	RoleSidecar   Role = "sidecar"
	RoleDecision  Role = "decision"
	RoleEmbedding Role = "embedding"
	RoleVision    Role = "vision"
	// RoleRabbit is the fast, cheap model used for retrieval/fan-out and
	// other latency-sensitive work. Optional; consumers fall back to sidecar
	// then main when it is unset.
	RoleRabbit Role = "rabbit"
)

// AllRoles is the ordered list of supported roles.
var AllRoles = []Role{RoleMain, RoleSidecar, RoleDecision, RoleEmbedding, RoleVision, RoleRabbit}

// Provider is a named, reusable model endpoint.
type Provider struct {
	Name     string
	Kind     ProviderKind
	Endpoint string
	APIKey   string
}

// RoleModel binds a role to a provider name and a model name.
type RoleModel struct {
	Provider string
	Model    string
}

// ParseProviderKind normalizes a provider kind string.
func ParseProviderKind(raw string) (ProviderKind, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ollama":
		return ProviderOllama, true
	case "openai", "openai-compatible":
		return ProviderOpenAI, true
	default:
		return "", false
	}
}

// KindForEndpoint infers the provider kind from an endpoint URL.
func KindForEndpoint(endpoint string) ProviderKind {
	if IsOpenAIEndpoint(endpoint) {
		return ProviderOpenAI
	}
	return ProviderOllama
}

// ParseRole normalizes a role name. "fast" is accepted as an alias for the
// fast rabbit role so users can name either way.
func ParseRole(raw string) (Role, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "fast" {
		value = string(RoleRabbit)
	}
	for _, role := range AllRoles {
		if string(role) == value {
			return role, true
		}
	}
	return "", false
}

// NormalizeProviderName lowercases and trims a provider name.
func NormalizeProviderName(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ValidProviderName reports whether a provider name is usable as a config key.
func ValidProviderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// CapabilityRequirement returns the model capability a role needs to work.
// An empty result means the role has no capability requirement.
func CapabilityRequirement(role Role) string {
	switch role {
	case RoleMain, RoleSidecar:
		return CapabilityCompletion
	case RoleDecision:
		return CapabilityDecision
	case RoleEmbedding:
		return CapabilityEmbedding
	case RoleVision:
		return CapabilityVision
	case RoleRabbit:
		return CapabilityCompletion
	default:
		return ""
	}
}

// RoleDescription returns a short human label for a role, used by command output.
func RoleDescription(role Role) string {
	switch role {
	case RoleMain:
		return "main chat model"
	case RoleSidecar:
		return "sidecar model"
	case RoleDecision:
		return "decision model"
	case RoleEmbedding:
		return "embedding model"
	case RoleVision:
		return "vision model"
	case RoleRabbit:
		return "fast retrieval model"
	default:
		return string(role)
	}
}

// ensureProviderMap lazily initializes the provider registry.
func (c *Config) ensureProviderMap() map[string]Provider {
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	return c.Providers
}

// ensureRoleMap lazily initializes the role bindings.
func (c *Config) ensureRoleMap() map[Role]RoleModel {
	if c.RoleModels == nil {
		c.RoleModels = map[Role]RoleModel{}
	}
	return c.RoleModels
}

// Provider returns the named provider from the registry.
func (c *Config) Provider(name string) (Provider, bool) {
	if c == nil {
		return Provider{}, false
	}
	name = NormalizeProviderName(name)
	if name == "" {
		name = DefaultProviderName
	}
	p, ok := c.Providers[name]
	return p, ok
}

// ProviderNames returns the configured provider names, ollama first then alphabetical.
func (c *Config) ProviderNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		if name == DefaultProviderName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if _, ok := c.Providers[DefaultProviderName]; ok {
		names = append([]string{DefaultProviderName}, names...)
	}
	return names
}

// AddProvider registers a new named provider.
func (c *Config) AddProvider(p Provider) error {
	name := NormalizeProviderName(p.Name)
	if !ValidProviderName(name) {
		return fmt.Errorf("invalid provider name %q: use letters, digits, dashes or underscores", p.Name)
	}
	if name == DefaultProviderName {
		return fmt.Errorf("provider %q already exists", name)
	}
	if _, exists := c.ensureProviderMap()[name]; exists {
		return fmt.Errorf("provider %q already exists", name)
	}
	if strings.TrimSpace(p.Endpoint) == "" {
		return fmt.Errorf("provider %q needs an endpoint", name)
	}
	if p.Kind == "" {
		p.Kind = KindForEndpoint(p.Endpoint)
	}
	p.Name = name
	c.Providers[name] = p
	return nil
}

// RemoveProvider deletes a provider. It refuses the default provider and any
// provider still bound to a role.
func (c *Config) RemoveProvider(name string) error {
	name = NormalizeProviderName(name)
	if name == DefaultProviderName {
		return fmt.Errorf("provider %q cannot be removed", DefaultProviderName)
	}
	if _, ok := c.ensureProviderMap()[name]; !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	for _, role := range AllRoles {
		if NormalizeProviderName(c.RoleModels[role].Provider) == name {
			return fmt.Errorf("provider %q is in use by the %s role", name, role)
		}
	}
	delete(c.Providers, name)
	return nil
}

// SetProviderEndpoint updates a provider endpoint, inferring its kind when unset.
func (c *Config) SetProviderEndpoint(name, endpoint string) error {
	name = NormalizeProviderName(name)
	if name == "" {
		name = DefaultProviderName
	}
	p, ok := c.ensureProviderMap()[name]
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return fmt.Errorf("endpoint cannot be empty")
	}
	p.Endpoint = endpoint
	if p.Kind == "" {
		p.Kind = KindForEndpoint(endpoint)
	}
	if p.Name == "" {
		p.Name = name
	}
	c.Providers[name] = p
	if name == DefaultProviderName {
		c.OllamaHost = endpoint
	}
	c.syncLegacyFields()
	return nil
}

// SetProviderKind overrides a provider kind.
func (c *Config) SetProviderKind(name string, kind ProviderKind) error {
	name = NormalizeProviderName(name)
	p, ok := c.ensureProviderMap()[name]
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	p.Kind = kind
	c.Providers[name] = p
	c.syncLegacyFields()
	return nil
}

// SetProviderAPIKey stores an API key for a provider.
func (c *Config) SetProviderAPIKey(name, key string) error {
	name = NormalizeProviderName(name)
	p, ok := c.ensureProviderMap()[name]
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	p.APIKey = strings.TrimSpace(key)
	c.Providers[name] = p
	c.syncLegacyFields()
	return nil
}

// RoleBinding returns the (provider, model) binding for a role.
func (c *Config) RoleBinding(role Role) RoleModel {
	if c == nil {
		return RoleModel{}
	}
	return c.RoleModels[role]
}

// RoleProvider returns the provider a role is bound to.
func (c *Config) RoleProvider(role Role) (Provider, bool) {
	binding := c.RoleBinding(role)
	if strings.TrimSpace(binding.Model) == "" {
		return Provider{}, false
	}
	return c.Provider(binding.Provider)
}

// RoleModelName returns the model name bound to a role.
func (c *Config) RoleModelName(role Role) string {
	return strings.TrimSpace(c.RoleBinding(role).Model)
}

// RoleInUse reports whether a role is bound to a model.
func (c *Config) RoleInUse(role Role) bool {
	return c.RoleModelName(role) != ""
}

// RoleStatus describes how a role is wired, in a form both the startup banner
// and command output can render. It carries data only, never formatting.
type RoleStatus struct {
	Role Role
	// Bound reports that the role has its own provider and model.
	Bound bool
	// Provider and Model are the bound binding, the fallback target when
	// Fallback is true, or empty when the role has neither.
	Provider string
	Model    string
	// Fallback reports that Provider and Model describe the target the role
	// uses while it stays unbound.
	Fallback bool
	// Note explains what a role without a model fallback does instead.
	Note string
}

// RoleStatuses is intentionally not exported as a slice helper; callers iterate
// AllRoles so the canonical order stays in one place.

// RoleStatus returns the wiring of one role: its own binding when set,
// otherwise the fallback target or the behavior it degrades to.
func (c *Config) RoleStatus(role Role) RoleStatus {
	if provider, model, ok := c.roleBinding(role); ok {
		return RoleStatus{Role: role, Bound: true, Provider: provider, Model: model}
	}
	if provider, model, ok := c.FallbackRoleModel(role); ok {
		return RoleStatus{Role: role, Provider: provider, Model: model, Fallback: true}
	}
	return RoleStatus{Role: role, Note: RoleBehaviorNote(role)}
}

// roleBinding reports the provider and model a role is bound to. It reads the
// role registry and falls back to the legacy flat fields, so configs built
// without a full Load (tests, early startup) still report their wiring. An
// unbound role reports ok=false.
func (c *Config) roleBinding(role Role) (provider, model string, ok bool) {
	if c == nil {
		return "", "", false
	}
	binding := c.RoleBinding(role)
	model = strings.TrimSpace(binding.Model)
	provider = NormalizeProviderName(binding.Provider)
	if model == "" {
		legacyModel, _, _ := c.legacyRoleFields(role)
		model = strings.TrimSpace(legacyModel)
		if model == "" {
			return "", "", false
		}
	}
	if provider == "" {
		provider = DefaultProviderName
	}
	return provider, model, true
}

// FallbackRoleModel returns the provider and model an unbound role falls back
// to at runtime: sidecar and rabbit fall back to main, decision falls back to
// the sidecar model and then to main. ok is false when the role has no model
// fallback (main, embedding, vision) or when the fallback target is unbound.
func (c *Config) FallbackRoleModel(role Role) (provider, model string, ok bool) {
	if c == nil {
		return "", "", false
	}
	switch role {
	case RoleSidecar, RoleRabbit:
		return c.roleBinding(RoleMain)
	case RoleDecision:
		if provider, model, ok := c.roleBinding(RoleSidecar); ok {
			return provider, model, true
		}
		return c.roleBinding(RoleMain)
	default:
		return "", "", false
	}
}

// RoleBehaviorNote explains what an unbound role does when it has no model to
// fall back to. It returns "" for roles that do have a fallback target.
func RoleBehaviorNote(role Role) string {
	switch role {
	case RoleEmbedding:
		return "text-only retrieval (no encoder)"
	case RoleVision:
		return "images go to main, then sidecar"
	default:
		return ""
	}
}

// RoleHint is the command that configures an unbound role.
func RoleHint(role Role) string {
	return fmt.Sprintf("(set it with /setmodel %s MODEL)", role)
}

// RoleTitle returns the label a role uses in the startup banner.
func RoleTitle(role Role) string {
	switch role {
	case RoleMain:
		return "Model"
	case RoleSidecar:
		return "Sidecar"
	case RoleDecision:
		return "Decision"
	case RoleEmbedding:
		return "Embedding"
	case RoleVision:
		return "Vision"
	case RoleRabbit:
		return "Rabbit"
	default:
		return string(role)
	}
}

// SetRoleModel binds a role to a provider and model. An empty model clears the role.
func (c *Config) SetRoleModel(role Role, provider, model string) error {
	if _, ok := ParseRole(string(role)); !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		delete(c.ensureRoleMap(), role)
		c.syncLegacyFields()
		return nil
	}
	provider = NormalizeProviderName(provider)
	if provider == "" {
		provider = DefaultProviderName
	}
	if _, ok := c.ensureProviderMap()[provider]; !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	c.RoleModels[role] = RoleModel{Provider: provider, Model: model}
	c.syncLegacyFields()
	return nil
}

// ClearRole unbinds a role.
func (c *Config) ClearRole(role Role) {
	delete(c.ensureRoleMap(), role)
	c.syncLegacyFields()
}

// markRoleOverride records that env or CLI set a role's legacy fields, so
// reconcileRegistry lets them win over config-file ROLE_* bindings.
func (c *Config) markRoleOverride(role Role) {
	if c.roleOverridden == nil {
		c.roleOverridden = map[Role]bool{}
	}
	c.roleOverridden[role] = true
}

// providerForEndpoint resolves the provider name that serves an endpoint,
// creating a provider named after the endpoint host when needed.
func (c *Config) providerForEndpoint(endpoint, kindHint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return DefaultProviderName
	}
	providers := c.ensureProviderMap()
	if op, ok := providers[DefaultProviderName]; ok && strings.TrimSpace(op.Endpoint) != "" {
		if sameEndpoint(op.Endpoint, endpoint) {
			return DefaultProviderName
		}
	}
	for name, p := range providers {
		if name == DefaultProviderName {
			continue
		}
		if sameEndpoint(p.Endpoint, endpoint) {
			return name
		}
	}
	kind := KindForEndpoint(endpoint)
	if parsed, ok := ParseProviderKind(kindHint); ok {
		kind = parsed
	}
	name := c.uniqueProviderName(providerNameFromEndpoint(endpoint))
	providers[name] = Provider{Name: name, Kind: kind, Endpoint: endpoint}
	return name
}

// uniqueProviderName appends a numeric suffix until the name is free.
func (c *Config) uniqueProviderName(base string) string {
	if base == "" {
		base = "custom"
	}
	if base == DefaultProviderName {
		base = "custom"
	}
	if _, exists := c.ensureProviderMap()[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, exists := c.ensureProviderMap()[candidate]; !exists {
			return candidate
		}
	}
}

// providerNameFromEndpoint derives a stable provider name from an endpoint URL.
func providerNameFromEndpoint(endpoint string) string {
	host := strings.TrimSpace(endpoint)
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		host = u.Host
	}
	host = strings.ToLower(host)
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-' || r == '_':
			b.WriteRune(r)
		case r == '.' || r == ':':
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-_")
	if name == "" {
		return "custom"
	}
	return name
}

// sameEndpoint compares two endpoint URLs ignoring a trailing slash.
func sameEndpoint(a, b string) bool {
	normalize := func(v string) string {
		return strings.TrimRight(strings.ToLower(strings.TrimSpace(v)), "/")
	}
	return normalize(a) != "" && normalize(a) == normalize(b)
}

// reconcileRegistry builds the provider registry and role bindings from the
// legacy flat fields and the new PROVIDER_*/ROLE_* keys.
func (c *Config) reconcileRegistry() {
	providers := c.ensureProviderMap()
	c.ensureRoleMap()

	// The default provider always exists and speaks native Ollama. Its endpoint
	// follows OLLAMA_HOST when that differs from the built-in default, so env and
	// CLI keep winning over the config file.
	op := providers[DefaultProviderName]
	op.Name = DefaultProviderName
	op.Kind = ProviderOllama
	if host := strings.TrimSpace(c.OllamaHost); host != "" && host != defaultOllamaHost {
		op.Endpoint = host
	} else if strings.TrimSpace(op.Endpoint) == "" {
		op.Endpoint = c.OllamaHost
	}
	if strings.TrimSpace(op.Endpoint) == "" {
		op.Endpoint = defaultOllamaHost
	}
	providers[DefaultProviderName] = op

	// Drop bindings that point at providers that no longer exist.
	for role, binding := range c.RoleModels {
		if binding.Model == "" {
			delete(c.RoleModels, role)
			continue
		}
		name := NormalizeProviderName(binding.Provider)
		if name == "" {
			name = DefaultProviderName
		}
		if _, ok := providers[name]; !ok {
			name = DefaultProviderName
		}
		binding.Provider = name
		c.RoleModels[role] = binding
	}

	// Fill missing bindings from the legacy flat fields (migration).
	for _, role := range AllRoles {
		if c.roleOverridden[role] {
			c.migrateRoleFromLegacy(role, true)
			continue
		}
		if strings.TrimSpace(c.RoleModels[role].Model) != "" {
			continue
		}
		c.migrateRoleFromLegacy(role, false)
	}

	c.syncLegacyFields()
}

// migrateRoleFromLegacy derives a role binding from the legacy flat fields.
// When keepProvider is true, a config-file provider binding survives unless the
// legacy endpoint explicitly overrides it.
func (c *Config) migrateRoleFromLegacy(role Role, keepProvider bool) {
	model, endpoint, kindHint := c.legacyRoleFields(role)
	model = strings.TrimSpace(model)
	endpoint = strings.TrimSpace(endpoint)
	if model == "" && endpoint == "" {
		return
	}
	provider := DefaultProviderName
	if keepProvider {
		if existing := NormalizeProviderName(c.RoleModels[role].Provider); existing != "" {
			provider = existing
		}
	}
	if endpoint != "" {
		provider = c.providerForEndpoint(endpoint, kindHint)
	}
	if model == "" {
		// An endpoint without a model still records the provider.
		c.RoleModels[role] = RoleModel{Provider: provider}
		return
	}
	c.RoleModels[role] = RoleModel{Provider: provider, Model: model}
}

// legacyRoleFields returns the legacy flat fields that describe a role.
func (c *Config) legacyRoleFields(role Role) (model, endpoint, kindHint string) {
	switch role {
	case RoleMain:
		return c.Model, c.ModelEndpoint, c.ModelProvider
	case RoleSidecar:
		return c.SidecarModel, c.SidecarEndpoint, c.SidecarProvider
	case RoleDecision:
		return c.DecisionModel, c.DecisionEndpoint, ""
	default:
		return "", "", ""
	}
}

// adoptLegacyFields makes the legacy flat fields authoritative for the roles
// they describe and projects the result onto the provider registry. It is used
// before persisting settings, so a caller that clears a legacy field clears the
// role it represents.
func (c *Config) adoptLegacyFields() {
	providers := c.ensureProviderMap()
	c.ensureRoleMap()
	op := providers[DefaultProviderName]
	op.Name = DefaultProviderName
	op.Kind = ProviderOllama
	if host := strings.TrimSpace(c.OllamaHost); host != "" {
		op.Endpoint = host
	} else if strings.TrimSpace(op.Endpoint) == "" {
		op.Endpoint = defaultOllamaHost
	}
	providers[DefaultProviderName] = op

	for _, role := range []Role{RoleMain, RoleSidecar, RoleDecision} {
		model, endpoint, kindHint := c.legacyRoleFields(role)
		model = strings.TrimSpace(model)
		endpoint = strings.TrimSpace(endpoint)
		if model == "" && endpoint == "" {
			delete(c.RoleModels, role)
			continue
		}
		provider := NormalizeProviderName(c.RoleModels[role].Provider)
		if provider == "" {
			provider = DefaultProviderName
		}
		if endpoint != "" {
			provider = c.providerForEndpoint(endpoint, kindHint)
		}
		c.RoleModels[role] = RoleModel{Provider: provider, Model: model}
	}

	c.syncLegacyFields()
}

// syncLegacyFields projects the provider registry and role bindings back onto
// the legacy flat fields so the rest of the runtime keeps working unchanged.
func (c *Config) syncLegacyFields() {
	providers := c.ensureProviderMap()
	if op, ok := providers[DefaultProviderName]; ok && strings.TrimSpace(op.Endpoint) != "" {
		c.OllamaHost = op.Endpoint
	}

	for _, role := range AllRoles {
		binding := c.RoleModels[role]
		provider, ok := providers[NormalizeProviderName(binding.Provider)]
		if !ok {
			provider = providers[DefaultProviderName]
			provider.Name = DefaultProviderName
			provider.Kind = ProviderOllama
		}
		model := strings.TrimSpace(binding.Model)
		// Only the default provider falls back to OllamaHost; a named provider
		// keeps its own endpoint even when it speaks the Ollama protocol.
		isDefault := provider.Name == DefaultProviderName

		endpoint, apiKey := "", ""
		kind := ProviderOllama
		if !isDefault {
			endpoint = provider.Endpoint
			apiKey = provider.APIKey
			kind = provider.Kind
		}

		switch role {
		case RoleMain:
			c.Model = model
			c.ModelEndpoint = endpoint
			c.ModelApiKey = apiKey
			c.ModelProvider = string(kind)
		case RoleSidecar:
			c.SidecarModel = model
			c.SidecarEndpoint = endpoint
			c.SidecarApiKey = apiKey
			c.SidecarProvider = string(kind)
		case RoleDecision:
			c.DecisionModel = model
			c.DecisionEndpoint = endpoint
			if apiKey != "" {
				c.DecisionAPIKey = apiKey
			}
		}
	}
}
