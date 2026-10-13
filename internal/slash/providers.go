package slash

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/openai"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// capabilityTagOrder lists the capability tags in a stable display order so a
// numbered model list reads the same way on every run.
var capabilityTagOrder = []string{
	config.CapabilityCompletion,
	config.CapabilityTools,
	config.CapabilityVision,
	config.CapabilityThinking,
	config.CapabilityEmbedding,
	config.CapabilityDecision,
}

// capabilityTagStyle gives every capability a stable color, so a model row can
// be scanned without reading the tag names.
func capabilityTagStyle(st tui.Style, tag string) string {
	switch tag {
	case config.CapabilityCompletion:
		return st.BrightGreen(tag)
	case config.CapabilityTools:
		return st.Green(tag)
	case config.CapabilityVision:
		return st.BoldCyan(tag)
	case config.CapabilityThinking:
		return st.Yellow(tag)
	case config.CapabilityEmbedding:
		return st.Magenta(tag)
	case config.CapabilityDecision:
		return st.BoldMagenta(tag)
	default:
		return st.Gray(tag)
	}
}

// coloredCapabilityTags renders each capability tag with its own color.
func coloredCapabilityTags(st tui.Style, tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, capabilityTagStyle(st, tag))
	}
	return out
}

// capabilityTagsFor reports the capability tags to show for one model. It
// prefers what the provider reports and falls back to the capability maps
// cached at startup, so the list keeps its meaning when a live call fails.
func capabilityTagsFor(m ollama.Model, cfg *config.Config) []string {
	reported := map[string]bool{}
	known := m.CapabilitiesKnown
	for _, capability := range m.Capabilities {
		capability = strings.ToLower(strings.TrimSpace(capability))
		if capability == "" {
			continue
		}
		reported[capability] = true
		known = true
	}
	if cfg != nil {
		for _, entry := range []struct {
			cache map[string]bool
			tag   string
		}{
			{cfg.ToolsByModel, config.CapabilityTools},
			{cfg.VisionByModel, config.CapabilityVision},
			{cfg.ThinkingByModel, config.CapabilityThinking},
		} {
			if value, ok := ollama.LookupVision(entry.cache, m.Name); ok {
				reported[entry.tag] = value
				known = true
			}
		}
	}
	if !known {
		return []string{"unknown"}
	}
	// A model that reports tool calling, thinking, or vision is a chat model
	// even when it omits the literal completion tag, and main and sidecar
	// require completion, so say it explicitly.
	if !reported[config.CapabilityCompletion] &&
		(reported[config.CapabilityTools] || reported[config.CapabilityThinking] || reported[config.CapabilityVision]) {
		reported[config.CapabilityCompletion] = true
	}
	var tags []string
	for _, tag := range capabilityTagOrder {
		if reported[tag] {
			tags = append(tags, tag)
		}
	}
	return tags
}

// providerClient builds a client for a provider, honoring its wire protocol.
// It returns nil when the provider has no endpoint yet.
func (c *Ctx) providerClient(p config.Provider) ollama.Client {
	endpoint := strings.TrimSpace(p.Endpoint)
	if endpoint == "" {
		return nil
	}
	if p.Kind == config.ProviderOpenAI {
		var opts []openai.Option
		if key := strings.TrimSpace(p.APIKey); key != "" {
			opts = append(opts, openai.WithAPIKey(key))
		}
		return openai.New(endpoint, opts...)
	}
	return ollama.NewHTTPWithAPIKey(ollama.NormalizeURL(endpoint), p.APIKey)
}

// providerModels lists the models a provider offers. The default provider
// reuses the shared client and its startup cache; a named provider gets its own
// client so two providers never share a connection by accident.
func (c *Ctx) providerModels(ctx context.Context, p config.Provider) ([]ollama.Model, error) {
	if config.NormalizeProviderName(p.Name) == config.DefaultProviderName {
		return c.installedModels(ctx), nil
	}
	client := c.providerClient(p)
	if client == nil {
		return nil, fmt.Errorf("no endpoint configured")
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return client.Tags(listCtx)
}

// rolesForModel reports which roles use a model on a provider.
func (c *Ctx) rolesForModel(providerName, modelName string) []string {
	if c.Cfg == nil {
		return nil
	}
	providerName = config.NormalizeProviderName(providerName)
	if providerName == "" {
		providerName = config.DefaultProviderName
	}
	modelName = strings.TrimSpace(modelName)
	var roles []string
	for _, role := range config.AllRoles {
		binding := c.Cfg.RoleBinding(role)
		if strings.TrimSpace(binding.Model) == "" {
			continue
		}
		bound := config.NormalizeProviderName(binding.Provider)
		if bound == "" {
			bound = config.DefaultProviderName
		}
		if bound != providerName {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(binding.Model), modelName) {
			roles = append(roles, string(role))
		}
	}
	return roles
}

// rolesOnProvider lists "role=model" pairs for the roles bound to a provider.
func (c *Ctx) rolesOnProvider(providerName string) []string {
	if c.Cfg == nil {
		return nil
	}
	providerName = config.NormalizeProviderName(providerName)
	if providerName == "" {
		providerName = config.DefaultProviderName
	}
	var pairs []string
	for _, role := range config.AllRoles {
		binding := c.Cfg.RoleBinding(role)
		model := strings.TrimSpace(binding.Model)
		if model == "" {
			continue
		}
		bound := config.NormalizeProviderName(binding.Provider)
		if bound == "" {
			bound = config.DefaultProviderName
		}
		if bound != providerName {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", role, model))
	}
	return pairs
}

// unassignedRoles lists the roles that have no model yet.
func (c *Ctx) unassignedRoles() []string {
	if c.Cfg == nil {
		return nil
	}
	var roles []string
	for _, role := range config.AllRoles {
		if strings.TrimSpace(c.Cfg.RoleModelName(role)) == "" {
			roles = append(roles, string(role))
		}
	}
	return roles
}

// roleNames returns the role names for error messages and usage hints.
func roleNames() string {
	names := make([]string, 0, len(config.AllRoles))
	for _, role := range config.AllRoles {
		names = append(names, string(role))
	}
	return strings.Join(names, ", ")
}

// printRoleStatus prints the (provider, model) binding of every role.
func printRoleStatus(c *Ctx) {
	st := tui.NewStyle(c.Out)
	fmt.Fprintln(c.Out, st.BoldCyan("Role assignments"))
	for _, role := range config.AllRoles {
		binding := c.Cfg.RoleBinding(role)
		model := strings.TrimSpace(binding.Model)
		if model == "" {
			fmt.Fprintf(c.Out, "  %s  %s\n", st.Bold(string(role)), st.Dim("(unset)"))
			continue
		}
		provider := config.NormalizeProviderName(binding.Provider)
		if provider == "" {
			provider = config.DefaultProviderName
		}
		fmt.Fprintf(c.Out, "  %s  %s  %s\n", st.Bold(string(role)), model, st.Dim("("+provider+")"))
	}
}

// runProvidersCommand lists the configured providers and the model each role
// uses, so one command answers "what is wired where".
func runProvidersCommand(c *Ctx, args []string) error {
	if len(args) > 0 {
		printProviderUsage(c)
		return nil
	}
	st := tui.NewStyle(c.Out)
	names := c.Cfg.ProviderNames()
	fmt.Fprintln(c.Out, st.BoldCyan(fmt.Sprintf("Providers (%d)", len(names))))
	for i, name := range names {
		p, _ := c.Cfg.Provider(name)
		line := fmt.Sprintf("  %s %s %s  %s",
			st.BoldBrightGreen(fmt.Sprintf("[%d]", i+1)),
			st.Bold(p.Name),
			st.Dim("("+string(p.Kind)+")"),
			p.Endpoint)
		if strings.TrimSpace(p.APIKey) != "" {
			line += "  " + st.Green("key: set")
		}
		fmt.Fprintln(c.Out, line)
		if roles := c.rolesOnProvider(name); len(roles) > 0 {
			fmt.Fprintf(c.Out, "      %s\n", st.Dim("roles: "+strings.Join(roles, " · ")))
		}
	}
	if unassigned := c.unassignedRoles(); len(unassigned) > 0 {
		fmt.Fprintln(c.Out)
		fmt.Fprintln(c.Out, st.Dim("Unassigned roles: "+strings.Join(unassigned, ", ")))
	}
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Use /provider add <NAME>, /models <PROVIDER>, /setmodel <PROVIDER> <role> <model>.")
	return nil
}

// printProviderUsage documents the provider command family.
func printProviderUsage(c *Ctx) {
	fmt.Fprintln(c.Out, "Usage:")
	fmt.Fprintln(c.Out, "  /providers                              list providers and the model each role uses")
	fmt.Fprintln(c.Out, "  /provider add <NAME> [endpoint]         register a new provider")
	fmt.Fprintln(c.Out, "  /provider <NAME> endpoint <url>         set the endpoint (creates the provider when new)")
	fmt.Fprintln(c.Out, "  /provider <NAME> key <api-key>          store an API key")
	fmt.Fprintln(c.Out, "  /provider <NAME> kind <ollama|openai>   override the detected protocol")
	fmt.Fprintln(c.Out, "  /provider <NAME> test                   check the endpoint and count its models")
	fmt.Fprintln(c.Out, "  /provider <NAME> remove                 delete an unused provider")
	fmt.Fprintf(c.Out, "  /models [PROVIDER]                      list a provider's models\n")
	fmt.Fprintf(c.Out, "  /setmodel [PROVIDER] <role> <number|name>  assign a model to a role (%s)\n", roleNames())
}

// validEndpointURL reports whether an endpoint is an absolute http(s) URL.
func validEndpointURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if _, err := url.ParseRequestURI(raw); err != nil {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// maybePromptProviderKey asks for an API key when a provider that needs one has
// none yet. The key is never echoed back.
func (c *Ctx) maybePromptProviderKey(p config.Provider) {
	if p.Kind != config.ProviderOpenAI || strings.TrimSpace(p.APIKey) != "" || c.Prompter == nil {
		return
	}
	answer, err := c.Prompter.GetInput(fmt.Sprintf("API key for %s (Enter to skip): ", p.Name))
	if err != nil {
		return
	}
	key := strings.TrimSpace(answer)
	if key == "" {
		return
	}
	if err := c.Cfg.SetProviderAPIKey(p.Name, key); err == nil {
		fmt.Fprintf(c.Out, "API key stored for %q.\n", p.Name)
	}
}

// runProviderCommand handles /provider and its subcommands.
func runProviderCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printProviderUsage(c)
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list", "ls":
		return runProvidersCommand(c, nil)
	case "add", "new":
		return addProviderCommand(c, args[1:])
	case "remove", "rm", "delete":
		return removeProviderCommand(c, args[1:])
	case "rename", "mv":
		fmt.Fprintln(c.Out, "Renaming a provider is not supported yet; remove it and add it again.")
		return nil
	case "endpoint", "url":
		return setProviderEndpointCommand(c, config.DefaultProviderName, args[1:])
	}

	name := config.NormalizeProviderName(args[0])
	if !config.ValidProviderName(name) {
		fmt.Fprintf(c.Out, "Invalid provider name %q: use letters, digits, dashes or underscores.\n", args[0])
		return nil
	}
	if len(args) == 1 {
		printProviderDetail(c, name)
		return nil
	}
	action := strings.ToLower(strings.TrimSpace(args[1]))
	rest := args[2:]
	switch action {
	case "endpoint", "url":
		return setProviderEndpointCommand(c, name, rest)
	case "key", "apikey", "api-key":
		return setProviderKeyCommand(c, name, rest)
	case "kind", "protocol":
		return setProviderKindCommand(c, name, rest)
	case "test", "check":
		return testProviderCommand(c, name)
	case "status", "info":
		printProviderDetail(c, name)
		return nil
	case "remove", "rm", "delete":
		return removeProviderCommand(c, []string{name})
	default:
		fmt.Fprintf(c.Out, "Unknown /provider action %q.\n", args[1])
		printProviderUsage(c)
		return nil
	}
}

// addProviderCommand registers a new named provider.
func addProviderCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(c.Out, "Usage: /provider add <NAME> [endpoint]")
		return nil
	}
	name := config.NormalizeProviderName(args[0])
	if !config.ValidProviderName(name) {
		fmt.Fprintf(c.Out, "Invalid provider name %q: use letters, digits, dashes or underscores.\n", args[0])
		return nil
	}
	endpoint := ""
	if len(args) > 1 {
		endpoint = strings.TrimSpace(args[1])
	}
	if endpoint == "" && c.Prompter != nil {
		answer, err := c.Prompter.GetInput(fmt.Sprintf("Endpoint for %s: ", name))
		if err != nil {
			return err
		}
		endpoint = strings.TrimSpace(answer)
	}
	if endpoint == "" {
		fmt.Fprintf(c.Out, "Provider %q was not added: run /provider %s endpoint <url> to create it.\n", name, name)
		return nil
	}
	if !validEndpointURL(endpoint) {
		fmt.Fprintf(c.Out, "Invalid endpoint %q: include the scheme and host, for example http://localhost:11434.\n", endpoint)
		return nil
	}
	if err := c.Cfg.AddProvider(config.Provider{Name: name, Endpoint: endpoint}); err != nil {
		fmt.Fprintf(c.Out, "Could not add provider %q: %v\n", name, err)
		return nil
	}
	p, _ := c.Cfg.Provider(name)
	fmt.Fprintf(c.Out, "Provider %q added (%s, %s). Run /save to persist.\n", p.Name, p.Kind, p.Endpoint)
	c.maybePromptProviderKey(p)
	return nil
}

// setProviderEndpointCommand sets an endpoint, creating the provider when the
// name is new so "/provider NAME endpoint <url>" works on its own.
func setProviderEndpointCommand(c *Ctx, name string, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(c.Out, "Usage: /provider %s endpoint <url>\n", name)
		return nil
	}
	endpoint := strings.TrimSpace(args[0])
	if !validEndpointURL(endpoint) {
		fmt.Fprintf(c.Out, "Invalid endpoint %q: include the scheme and host, for example http://localhost:11434.\n", endpoint)
		return nil
	}
	if _, ok := c.Cfg.Provider(name); !ok {
		if err := c.Cfg.AddProvider(config.Provider{Name: name, Endpoint: endpoint}); err != nil {
			fmt.Fprintf(c.Out, "Could not set the endpoint for %q: %v\n", name, err)
			return nil
		}
	} else if err := c.Cfg.SetProviderEndpoint(name, endpoint); err != nil {
		fmt.Fprintf(c.Out, "Could not set the endpoint for %q: %v\n", name, err)
		return nil
	}
	p, _ := c.Cfg.Provider(name)
	fmt.Fprintf(c.Out, "Provider %q endpoint set to %s (%s). Run /save to persist.\n", p.Name, p.Endpoint, p.Kind)
	c.maybePromptProviderKey(p)
	return nil
}

// setProviderKeyCommand stores a provider API key without echoing it back.
func setProviderKeyCommand(c *Ctx, name string, args []string) error {
	if _, ok := c.Cfg.Provider(name); !ok {
		fmt.Fprintf(c.Out, "Unknown provider %q. Configured providers: %s.\n", name, strings.Join(c.Cfg.ProviderNames(), ", "))
		return nil
	}
	key := ""
	if len(args) > 0 {
		key = strings.TrimSpace(args[0])
	}
	if key == "" {
		if c.Prompter == nil {
			fmt.Fprintf(c.Out, "Usage: /provider %s key <api-key>\n", name)
			return nil
		}
		answer, err := c.Prompter.GetInput(fmt.Sprintf("API key for %s: ", name))
		if err != nil {
			return err
		}
		key = strings.TrimSpace(answer)
	}
	if key == "" {
		fmt.Fprintln(c.Out, "No API key stored.")
		return nil
	}
	if err := c.Cfg.SetProviderAPIKey(name, key); err != nil {
		fmt.Fprintf(c.Out, "Could not store the API key: %v\n", err)
		return nil
	}
	fmt.Fprintf(c.Out, "API key stored for %q. Run /save to persist.\n", name)
	return nil
}

// setProviderKindCommand overrides the protocol detected from the endpoint.
func setProviderKindCommand(c *Ctx, name string, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(c.Out, "Usage: /provider %s kind <ollama|openai>\n", name)
		return nil
	}
	kind, ok := config.ParseProviderKind(args[0])
	if !ok {
		fmt.Fprintf(c.Out, "Unknown kind %q. Use ollama or openai.\n", args[0])
		return nil
	}
	if err := c.Cfg.SetProviderKind(name, kind); err != nil {
		fmt.Fprintf(c.Out, "Could not set the kind for %q: %v\n", name, err)
		return nil
	}
	fmt.Fprintf(c.Out, "Provider %q kind set to %s. Run /save to persist.\n", name, kind)
	return nil
}

// testProviderCommand probes a provider endpoint and reports what it found.
func testProviderCommand(c *Ctx, name string) error {
	p, ok := c.Cfg.Provider(name)
	if !ok {
		fmt.Fprintf(c.Out, "Unknown provider %q. Configured providers: %s.\n", name, strings.Join(c.Cfg.ProviderNames(), ", "))
		return nil
	}
	client := c.providerClient(p)
	if client == nil {
		fmt.Fprintf(c.Out, "Provider %q has no endpoint configured.\n", p.Name)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := client.Tags(ctx)
	if err != nil {
		fmt.Fprintf(c.Out, "Provider %q is not reachable at %s: %v\n", p.Name, p.Endpoint, err)
		return nil
	}
	message := fmt.Sprintf("Provider %q is reachable at %s: %d models", p.Name, p.Endpoint, len(models))
	if prober, ok := client.(interface {
		Version(context.Context) (string, error)
	}); ok {
		if version, err := prober.Version(ctx); err == nil {
			if version = strings.TrimSpace(version); version != "" {
				message += ", version " + version
			}
		}
	}
	fmt.Fprintln(c.Out, message+".")
	return nil
}

// printProviderDetail shows one provider and the roles it serves.
func printProviderDetail(c *Ctx, name string) {
	p, ok := c.Cfg.Provider(name)
	if !ok {
		fmt.Fprintf(c.Out, "Unknown provider %q. Configured providers: %s.\n", name, strings.Join(c.Cfg.ProviderNames(), ", "))
		return
	}
	st := tui.NewStyle(c.Out)
	fmt.Fprintf(c.Out, "%s %s  %s\n", st.Bold(p.Name), st.Dim("("+string(p.Kind)+")"), p.Endpoint)
	if strings.TrimSpace(p.APIKey) != "" {
		fmt.Fprintln(c.Out, "  API key: set")
	} else {
		fmt.Fprintln(c.Out, "  API key: not set")
	}
	if roles := c.rolesOnProvider(p.Name); len(roles) > 0 {
		fmt.Fprintln(c.Out, "  roles: "+strings.Join(roles, " · "))
	} else {
		fmt.Fprintln(c.Out, "  roles: none")
	}
	fmt.Fprintf(c.Out, "  models: run /models %s\n", p.Name)
}

// removeProviderCommand deletes an unused provider.
func removeProviderCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(c.Out, "Usage: /provider remove <NAME>")
		return nil
	}
	name := config.NormalizeProviderName(args[0])
	if err := c.Cfg.RemoveProvider(name); err != nil {
		fmt.Fprintf(c.Out, "Could not remove provider %q: %v\n", name, err)
		return nil
	}
	fmt.Fprintf(c.Out, "Provider %q removed. Run /save to persist.\n", name)
	return nil
}

// runModelsCommand lists a provider's models, numbered, with colored capability
// tags and the roles currently using each one.
func runModelsCommand(c *Ctx, args []string) error {
	if len(args) > 1 {
		fmt.Fprintln(c.Out, "Usage: /models [PROVIDER] (PROVIDER defaults to ollama)")
		return nil
	}
	name := config.DefaultProviderName
	if len(args) == 1 {
		name = config.NormalizeProviderName(args[0])
	}
	provider, ok := c.Cfg.Provider(name)
	if !ok {
		fmt.Fprintf(c.Out, "Unknown provider %q. Configured providers: %s.\n", name, strings.Join(c.Cfg.ProviderNames(), ", "))
		return nil
	}
	models, err := c.providerModels(context.Background(), provider)
	if err != nil {
		fmt.Fprintf(c.Out, "Could not list models from %s (%s): %v\n", provider.Name, provider.Endpoint, err)
		return nil
	}
	if len(models) == 0 {
		if provider.Name == config.DefaultProviderName {
			fmt.Fprintln(c.Out, "No installed models reported by /api/tags.")
		} else {
			fmt.Fprintf(c.Out, "No models reported by %s (%s).\n", provider.Name, provider.Endpoint)
		}
		return nil
	}
	c.printModelRows(models, fmt.Sprintf("Models on %s (%d)", provider.Name, len(models)), func(m ollama.Model) string {
		roles := c.rolesForModel(provider.Name, m.Name)
		if len(roles) == 0 {
			return ""
		}
		return "(" + strings.Join(roles, " · ") + ")"
	})
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Assign a model with /setmodel <provider> <role> <number|name>; run /save to persist.")
	return nil
}

// resolveModelChoice maps a numbered or named argument to a model. A number out
// of range reports false instead of being treated as a model name.
func resolveModelChoice(arg string, models []ollama.Model) (ollama.Model, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return ollama.Model{}, false
	}
	if idx, ok := parseModelChoiceIndex(arg, len(models)); ok {
		return models[idx], true
	}
	if !allDigits(arg) && modelNameRe.MatchString(arg) {
		return ollama.Model{Name: arg}, true
	}
	return ollama.Model{}, false
}

// isClearChoice reports whether an argument means "unbind this role".
func isClearChoice(arg string) bool {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "off", "none", "disable", "disabled", "clear":
		return true
	}
	return false
}

// refreshCapabilityFlags re-reads the cached capability maps for the role that
// just changed, so the reported flags match the new model.
func refreshCapabilityFlags(c *Ctx, role config.Role, name string) {
	available, known := ollama.LookupVision(c.Cfg.VisionByModel, name)
	switch role {
	case config.RoleMain:
		c.Cfg.VisionAvailable, c.Cfg.VisionKnown = available, known
		refreshThinkingFlags(c)
		refreshToolsFlags(c)
	case config.RoleSidecar:
		c.Cfg.SidecarVisionAvailable, c.Cfg.SidecarVisionKnown = available, known
	}
}

// applyRoleModel validates a model against the role requirement and binds it.
func (c *Ctx) applyRoleModel(provider config.Provider, role config.Role, model ollama.Model) error {
	name := strings.TrimSpace(model.Name)
	if name == "" {
		fmt.Fprintln(c.Out, "Model name cannot be empty.")
		return nil
	}
	status := config.CheckModelForRole(provider.Kind, role, model)
	if status.Verdict == config.CapabilityUnsupported {
		fmt.Fprintf(c.Out, "%s does not report the %q capability required by the %s role (%s). Nothing changed.\n",
			name, status.Required, role, config.RoleDescription(role))
		return nil
	}
	if err := c.Cfg.SetRoleModel(role, provider.Name, name); err != nil {
		fmt.Fprintf(c.Out, "Could not set the %s role: %v\n", role, err)
		return nil
	}
	switch role {
	case config.RoleMain, config.RoleSidecar:
		probeCapabilities(c, name)
		refreshCapabilityFlags(c, role, name)
	}
	if role == config.RoleSidecar {
		c.Cfg.SidecarDisabled = false
		c.Cfg.SidecarSkipSession = false
	}
	note := ""
	switch status.Verdict {
	case config.CapabilityUnverified:
		note = fmt.Sprintf(" (%s capability unverified on provider %q)", status.Required, provider.Name)
	case config.CapabilityOK:
		note = fmt.Sprintf(" (%s capability verified)", status.Required)
	}
	fmt.Fprintf(c.Out, "%s role set to %s on %s%s. Run /save to persist.\n", role, name, provider.Name, note)
	return nil
}

// pickProviderModelForRole shows a provider's numbered models and asks which one
// the role should use.
func (c *Ctx) pickProviderModelForRole(ctx context.Context, provider config.Provider, role config.Role) error {
	models, err := c.providerModels(ctx, provider)
	if err != nil {
		fmt.Fprintf(c.Out, "Could not list models from %s (%s): %v\n", provider.Name, provider.Endpoint, err)
		return nil
	}
	if len(models) == 0 {
		fmt.Fprintf(c.Out, "No models reported by %s (%s).\n", provider.Name, provider.Endpoint)
		return nil
	}
	current := c.Cfg.RoleModelName(role)
	c.printModelRows(models, fmt.Sprintf("Models on %s (%d)", provider.Name, len(models)), func(m ollama.Model) string {
		status := config.CheckModelForRole(provider.Kind, role, m)
		switch {
		case strings.EqualFold(strings.TrimSpace(m.Name), current):
			return "(current)"
		case status.Verdict == config.CapabilityUnsupported:
			return "(no " + status.Required + ")"
		default:
			return ""
		}
	})
	if c.Prompter == nil {
		fmt.Fprintf(c.Out, "Use /setmodel %s %s <name> to set it.\n", provider.Name, role)
		return nil
	}
	answer, err := c.Prompter.GetInput(string(role) + " choice: ")
	if err != nil {
		return err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		fmt.Fprintf(c.Out, "%s unchanged.\n", role)
		return nil
	}
	model, ok := resolveModelChoice(answer, models)
	if !ok {
		fmt.Fprintln(c.Out, "Invalid choice. Enter a listed number or a model name.")
		return nil
	}
	return c.applyRoleModel(provider, role, model)
}

// runSetModelCommand binds a model to one of the five roles.
//
// Grammar: /setmodel [PROVIDER] <role> <number|name>. With no provider it uses
// ollama, so "/setmodel main 5" keeps working; "off|none|clear" unbinds.
func runSetModelCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printRoleStatus(c)
		fmt.Fprintln(c.Out)
		fmt.Fprintf(c.Out, "Usage: /setmodel [PROVIDER] <role> <number|name>  (roles: %s)\n", roleNames())
		return nil
	}
	providerName := config.DefaultProviderName
	rest := args
	if _, ok := config.ParseRole(args[0]); !ok {
		providerName = config.NormalizeProviderName(args[0])
		rest = args[1:]
	}
	if len(rest) == 0 {
		fmt.Fprintf(c.Out, "Usage: /setmodel %s <role> <number|name>\n", providerName)
		return nil
	}
	role, ok := config.ParseRole(rest[0])
	if !ok {
		fmt.Fprintf(c.Out, "Unknown role %q. Valid roles: %s.\n", rest[0], roleNames())
		return nil
	}
	provider, ok := c.Cfg.Provider(providerName)
	if !ok {
		fmt.Fprintf(c.Out, "Unknown provider %q. Configured providers: %s.\n", providerName, strings.Join(c.Cfg.ProviderNames(), ", "))
		return nil
	}
	if len(rest) == 1 {
		return c.pickProviderModelForRole(context.Background(), provider, role)
	}
	arg := strings.TrimSpace(rest[1])
	if isClearChoice(arg) {
		c.Cfg.ClearRole(role)
		fmt.Fprintf(c.Out, "%s role cleared. Run /save to persist.\n", role)
		return nil
	}
	models, err := c.providerModels(context.Background(), provider)
	if err != nil {
		models = nil
		if allDigits(arg) {
			fmt.Fprintf(c.Out, "Could not list models from %s (%s): %v\n", provider.Name, provider.Endpoint, err)
			return nil
		}
	}
	model, ok := resolveModelChoice(arg, models)
	if !ok {
		if allDigits(arg) {
			fmt.Fprintf(c.Out, "No model number %s in the list from %s.\n", arg, provider.Name)
		} else {
			fmt.Fprintf(c.Out, "Invalid model name %q.\n", arg)
		}
		return nil
	}
	return c.applyRoleModel(provider, role, model)
}
