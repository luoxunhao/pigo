package agent

// config is the resolved, unexported construction state for a Session. It is
// populated only through Option values, so callers never name or mutate it
// directly — the exported surface stays limited to With* constructors and the
// Session methods.
type config struct {
	model              string
	baseURL            string
	protocol           string
	provider           string
	apiKey             string
	systemPrompt       string
	appendSystemPrompt []string
	thinking           string
	noTools            bool
	allowedTools       []string
	disallowedTools    []string
	skills             bool
	memory             bool
}

// Option configures a Session at construction time. Options are applied in the
// order passed to New, so a later option overrides an earlier one that sets the
// same field. Because config is unexported, the only way to produce an Option is
// through the With* constructors below — which keeps the public surface free of
// internal types.
type Option func(*config)

// WithModel sets the model id. It must match a configured [[models]] entry in
// config.toml (provider/model_id), the same source the pigo CLI uses. The
// default is "openrouter/free", which resolves only when such an entry exists.
func WithModel(model string) Option {
	return func(c *config) { c.model = model }
}

// WithBaseURL points the session at a custom endpoint. Pair it with
// [WithProtocol] to say whether that endpoint speaks the OpenAI or Anthropic
// wire format.
func WithBaseURL(baseURL string) Option {
	return func(c *config) { c.baseURL = baseURL }
}

// WithProtocol selects the wire protocol for a custom endpoint: "openai" or
// "anthropic". It is only consulted when [WithBaseURL] is set.
func WithProtocol(protocol string) Option {
	return func(c *config) { c.protocol = protocol }
}

// WithProvider supplies the provider prefix for a configured [[models]] entry
// when the model id does not already carry one (e.g. WithModel("free") plus
// WithProvider("openrouter") selects the entry "openrouter/free").
func WithProvider(name string) Option {
	return func(c *config) { c.provider = name }
}

// WithAPIKey sets the API key for the resolved provider, overriding the
// provider's environment variable. When unset, the provider's usual environment
// variable is used (e.g. ANTHROPIC_API_KEY, OPENROUTER_API_KEY).
func WithAPIKey(key string) Option {
	return func(c *config) { c.apiKey = key }
}

// WithSystemPrompt replaces pigo's built-in base instruction with prompt. Use
// this for full control over the agent's persona and rules; use
// [WithAppendSystemPrompt] instead to keep the built-in instruction and add to
// it.
func WithSystemPrompt(prompt string) Option {
	return func(c *config) { c.systemPrompt = prompt }
}

// WithAppendSystemPrompt appends one or more blocks to the system prompt,
// leaving pigo's built-in instruction in place. Repeated calls accumulate.
func WithAppendSystemPrompt(blocks ...string) Option {
	return func(c *config) {
		c.appendSystemPrompt = append(c.appendSystemPrompt, blocks...)
	}
}

// WithThinkingLevel sets the reasoning-effort level. Valid values are "off",
// "minimal", "low", "medium", "high", "xhigh", and "max". The default is
// "medium". An invalid value makes New return an error.
func WithThinkingLevel(level string) Option {
	return func(c *config) { c.thinking = level }
}

// WithTools restricts the session to the named built-in tools (an allowlist,
// e.g. WithTools("read", "grep")). Names are matched case-insensitively, so
// "Read" and "read" are equivalent. A name that matches no tool makes New
// return an error rather than silently ignoring it. Combine with
// [WithDisallowedTools]; deny always wins over allow.
func WithTools(names ...string) Option {
	return func(c *config) { c.allowedTools = append(c.allowedTools, names...) }
}

// WithDisallowedTools removes the named built-in tools (a denylist, e.g.
// WithDisallowedTools("bash")). Deny always wins: a tool named here is removed
// even if it also appears in [WithTools]. As with WithTools, an unknown name
// makes New return an error.
func WithDisallowedTools(names ...string) Option {
	return func(c *config) { c.disallowedTools = append(c.disallowedTools, names...) }
}

// WithoutTools removes every tool, producing a pure text-completion session that
// cannot touch the filesystem or run commands. It overrides [WithTools] and
// [WithDisallowedTools], which become inert once the tool set is empty.
func WithoutTools() Option {
	return func(c *config) { c.noTools = true }
}

// WithSkills enables discovery of on-disk skills, which are advertised to the
// model and loadable during a run. Skills are off by default so an embedded
// session stays independent of the machine's shared skills directory.
func WithSkills() Option {
	return func(c *config) { c.skills = true }
}

// WithMemory enables pigo's persistent memory store, letting the agent recall
// context saved by earlier runs and record new memories. Memory is off by
// default so an embedded session does not read or write shared state unless
// asked.
func WithMemory() Option {
	return func(c *config) { c.memory = true }
}
