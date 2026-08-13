// Package contextbuild owns the pigo context-construction pipeline. It aligns
// with pi's harness: BuildSessionContext projects persisted entries into a
// session context, and BuildProviderContext assembles one provider-visible
// request (system prompt, messages, tools, model state) through injected
// dependencies.
package contextbuild

import (
	"context"
	"errors"
	"fmt"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
)

// EntryProjector turns one custom entry into zero or more messages. It is a
// non-error seam: panics are recovered and the projector's output is skipped.
type EntryProjector func(entry session.V4Entry, index int, entries []session.V4Entry) []agentcore.Message

// TransformContextFunc rewrites the request message copy. It must not error;
// implementations return the original list as a safe fallback on failure.
type TransformContextFunc func(ctx context.Context, msgs agentcore.MessageList) agentcore.MessageList

// ConvertToLlmFunc filters/shapes UI-only messages into LLM-bound messages.
// It must not error.
type ConvertToLlmFunc func(msgs agentcore.MessageList) agentcore.MessageList

// SessionBuildOptions carries per-session projection options.
type SessionBuildOptions struct {
	EntryProjectors map[string]EntryProjector
	// Registry, when non-nil, is consulted for custom-entry projectors in
	// addition to EntryProjectors. It lets frontends build one session-level
	// registry (built-in + hooks + plugin declarations) and reuse it for both
	// projection and per-request transforms.
	Registry *Registry
}

// SessionContext is the pure projection result: LLM-bound messages plus the
// lane.config state. ActiveToolNames == nil means "all tools".
type SessionContext struct {
	Messages        agentcore.MessageList
	Model           string
	Provider        string
	ThinkingLevel   agentcore.ThinkingLevel
	ActiveToolNames []string
}

// PromptBuildOptions feeds BuildSystemPrompt. The zero value is usable.
type PromptBuildOptions struct {
	BaseInstruction     string
	WorkingDir          string
	GlobalAgentDir      string
	ContextFilesEnabled bool
	AppendInstructions  []string
	Skills              []*runtime.Skill
	ActiveToolNames     []string
	Tools               []agentcore.AgentTool
	ReadFile            func(path string) ([]byte, error)
	IsWorktreeRoot      func(dir string) bool
}

// PromptBuilderFunc is the injected system-prompt builder seam.
type PromptBuilderFunc func(ctx context.Context, opts PromptBuildOptions) (string, error)

// ToolResolverFunc resolves the active tool set for a session context.
type ToolResolverFunc func(ctx context.Context, sess *SessionContext) ([]agentcore.AgentTool, error)

// BuildDeps are the injected I/O seams of BuildProviderContext.
type BuildDeps struct {
	PromptBuilder PromptBuilderFunc
	ToolResolver  ToolResolverFunc
	Registry      *Registry
	Transform     TransformContextFunc
	Convert       ConvertToLlmFunc
	Reminders     *runtime.ReminderRegistry
	BlockImages   bool
}

// RequestOptions are per-request inputs that are not part of the session
// projection (cwd, system-prompt sources, the full tool set).
type RequestOptions struct {
	Cwd                 string
	BaseInstruction     string
	AppendInstructions  []string
	ContextFilesEnabled bool
	Skills              []*runtime.Skill
	AllTools            []agentcore.AgentTool
	GlobalAgentDir      string
	ReadFile            func(path string) ([]byte, error)
	IsWorktreeRoot      func(dir string) bool
}

// ProviderRequest is the provider-visible request. It never carries an API
// key; key resolution stays in the runtime/credential layer.
type ProviderRequest struct {
	Model         string
	Provider      string
	ThinkingLevel agentcore.ThinkingLevel
	LlmContext    provider.LlmContext
}

// Builder owns per-process caches (currently the system-prompt fingerprint
// cache). Zero value is usable.
type Builder struct {
	prompts *promptCache
}

// NewBuilder returns a Builder with caches initialized.
func NewBuilder() *Builder {
	return &Builder{prompts: newPromptCache()}
}

// BuildSessionContext projects a ProjectLeaf into a SessionContext. Custom
// entries are projected only through registered EntryProjectors; unregistered
// custom types are skipped. Build failure returns an error.
func BuildSessionContext(proj *session.ProjectLeaf, opts SessionBuildOptions) (*SessionContext, error) {
	if proj == nil {
		return nil, errors.New("contextbuild: nil project leaf")
	}
	ctx := &SessionContext{
		Model:           proj.Model,
		Provider:        proj.Provider,
		ThinkingLevel:   agentcore.ThinkingLevel(proj.ThinkingLevel),
		ActiveToolNames: nil,
	}
	if proj.Config != nil {
		ctx.ActiveToolNames = proj.Config.ActiveToolNames
	}
	entries := proj.Entries
	for i := range entries {
		e := entries[i]
		switch e.Type {
		case session.EntryTypeMessage, session.EntryTypeCompaction, session.EntryTypeBranchSummary:
			msg, err := e.MessageValue()
			if err != nil {
				return nil, fmt.Errorf("contextbuild: project entry %s: %w", e.ID, err)
			}
			if a, ok := msg.(agentcore.AssistantMessage); ok {
				if a.StopReason == agentcore.StopReasonError || a.StopReason == agentcore.StopReasonAborted {
					continue
				}
			}
			ctx.Messages = append(ctx.Messages, msg)
		case session.EntryTypeCustom:
			projector, ok := opts.EntryProjectors[e.CustomType]
			if !ok && opts.Registry != nil {
				projector, ok = opts.Registry.Projector(e.CustomType)
			}
			if !ok {
				continue
			}
			ctx.Messages = append(ctx.Messages, safeProject(projector, e, i, entries)...)
		}
	}
	return ctx, nil
}

// safeProject recovers panics from a projector and falls back to no output.
func safeProject(fn EntryProjector, entry session.V4Entry, index int, entries []session.V4Entry) (out []agentcore.Message) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return fn(entry, index, entries)
}

// BuildProviderContext is the orchestration entry: resolve model/tools,
// assemble the system prompt (fingerprint-cached), run the transform chain,
// convert to LLM messages, and return the provider request. I/O goes through
// deps; the package-level function uses a fresh Builder (no cross-call cache).
func BuildProviderContext(ctx context.Context, sess *SessionContext, deps BuildDeps, req RequestOptions) (*ProviderRequest, error) {
	return NewBuilder().BuildProviderContext(ctx, sess, deps, req)
}

// BuildProviderContext implements the orchestration on a Builder.
func (b *Builder) BuildProviderContext(ctx context.Context, sess *SessionContext, deps BuildDeps, req RequestOptions) (*ProviderRequest, error) {
	if sess == nil {
		return nil, errors.New("contextbuild: nil session context")
	}
	tools, err := resolveTools(ctx, deps.ToolResolver, sess, req.AllTools)
	if err != nil {
		return nil, err
	}
	prompt, err := b.buildPrompt(ctx, deps, req, tools)
	if err != nil {
		return nil, err
	}
	msgs := sess.Messages
	if deps.Registry != nil {
		msgs = deps.Registry.ApplyTransforms(ctx, msgs)
	}
	if deps.Transform != nil {
		msgs = deps.Transform(ctx, msgs)
	}
	if deps.Reminders != nil {
		msgs = ReminderTransform(deps.Reminders)(ctx, msgs)
	}
	convert := deps.Convert
	if convert == nil {
		convert = ConvertToLlm
	}
	msgs = convert(msgs)
	if deps.BlockImages {
		msgs = ConvertToLlmWithOptions(msgs, ConvertOptions{BlockImages: true})
	}
	return &ProviderRequest{
		Model:         sess.Model,
		Provider:      sess.Provider,
		ThinkingLevel: sess.ThinkingLevel,
		LlmContext: provider.LlmContext{
			SystemPrompt: prompt,
			Messages:     msgs,
			Tools:        tools,
		},
	}, nil
}

func resolveTools(ctx context.Context, resolver ToolResolverFunc, sess *SessionContext, all []agentcore.AgentTool) ([]agentcore.AgentTool, error) {
	if resolver != nil {
		tools, err := resolver(ctx, sess)
		if err != nil {
			return nil, err
		}
		if tools != nil {
			return tools, nil
		}
	}
	if sess.ActiveToolNames == nil {
		return all, nil
	}
	allowed := make(map[string]bool, len(sess.ActiveToolNames))
	for _, name := range sess.ActiveToolNames {
		allowed[name] = true
	}
	out := make([]agentcore.AgentTool, 0, len(all))
	for _, t := range all {
		if allowed[t.Name()] {
			out = append(out, t)
		}
	}
	return out, nil
}

func (b *Builder) buildPrompt(ctx context.Context, deps BuildDeps, req RequestOptions, tools []agentcore.AgentTool) (string, error) {
	cfg := PromptBuildOptions{
		BaseInstruction:     req.BaseInstruction,
		WorkingDir:          req.Cwd,
		GlobalAgentDir:      req.GlobalAgentDir,
		ContextFilesEnabled: req.ContextFilesEnabled,
		AppendInstructions:  req.AppendInstructions,
		Skills:              req.Skills,
		ActiveToolNames:     toolNames(tools),
		Tools:               tools,
		ReadFile:            req.ReadFile,
		IsWorktreeRoot:      req.IsWorktreeRoot,
	}
	if deps.PromptBuilder != nil {
		return deps.PromptBuilder(ctx, cfg)
	}
	if b == nil || b.prompts == nil {
		return BuildSystemPrompt(cfg)
	}
	return b.prompts.get(cfg)
}

func toolNames(tools []agentcore.AgentTool) []string {
	if len(tools) == 0 {
		return nil
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

// RequestBuilder adapts a session context + deps into the runtime loop's
// per-request seam. Each turn it swaps in the live message list, rebuilds the
// provider request through BuildProviderContext, and returns the
// provider-visible LlmContext. An error is surfaced by the loop as a terminal
// assistant message.
func RequestBuilder(sess *SessionContext, deps BuildDeps, req RequestOptions) runtime.RequestBuilderFunc {
	return func(ctx context.Context, msgs agentcore.MessageList) (provider.LlmContext, error) {
		if sess == nil {
			return provider.LlmContext{}, errors.New("contextbuild: nil session context")
		}
		sess.Messages = msgs
		pr, err := BuildProviderContext(ctx, sess, deps, req)
		if err != nil {
			return provider.LlmContext{}, err
		}
		return pr.LlmContext, nil
	}
}
