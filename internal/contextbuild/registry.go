package contextbuild

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/plugin"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
)

// Registry is the session-level typed extension registry. Transforms run in
// registration order; entry projectors are keyed by customType and reject
// duplicates.
type Registry struct {
	mu         sync.Mutex
	transforms []namedTransform
	projectors map[string]EntryProjector
}

type namedTransform struct {
	name string
	fn   TransformContextFunc
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{projectors: map[string]EntryProjector{}}
}

// RegisterTransform appends a transform. Duplicate names are allowed (order
// is the contract); a nil function is rejected.
func (r *Registry) RegisterTransform(name string, fn TransformContextFunc) error {
	if fn == nil {
		return fmt.Errorf("contextbuild: transform %q is nil", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transforms = append(r.transforms, namedTransform{name: name, fn: fn})
	return nil
}

// RegisterEntryProjector registers a projector by customType. Duplicate keys
// are rejected fail-closed.
func (r *Registry) RegisterEntryProjector(customType string, fn EntryProjector) error {
	if customType == "" {
		return fmt.Errorf("contextbuild: empty custom type")
	}
	if fn == nil {
		return fmt.Errorf("contextbuild: projector for %q is nil", customType)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.projectors[customType]; ok {
		return fmt.Errorf("contextbuild: duplicate entry projector for %q", customType)
	}
	r.projectors[customType] = fn
	return nil
}

// Projector returns the projector registered for customType.
func (r *Registry) Projector(customType string) (EntryProjector, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn, ok := r.projectors[customType]
	return fn, ok
}

// ApplyTransforms runs every registered transform in order. Each transform
// sees the previous output; panics are recovered and the input is kept.
func (r *Registry) ApplyTransforms(ctx context.Context, msgs agentcore.MessageList) agentcore.MessageList {
	r.mu.Lock()
	transforms := append([]namedTransform(nil), r.transforms...)
	r.mu.Unlock()
	for _, t := range transforms {
		msgs = safeTransform(t.fn, ctx, msgs)
	}
	return msgs
}

func safeTransform(fn TransformContextFunc, ctx context.Context, msgs agentcore.MessageList) (out agentcore.MessageList) {
	defer func() {
		if recover() != nil {
			out = msgs
		}
	}()
	return fn(ctx, msgs)
}

// ReminderTransform adapts the runtime reminder registry to a contextbuild
// transform. It injects ephemeral reminders into the request copy only.
func ReminderTransform(reg *runtime.ReminderRegistry) TransformContextFunc {
	return func(ctx context.Context, msgs agentcore.MessageList) agentcore.MessageList {
		if reg == nil || reg.Empty() {
			return msgs
		}
		rem := reg.Messages(ctx, msgs)
		if len(rem) == 0 {
			return msgs
		}
		out := make(agentcore.MessageList, 0, len(msgs)+len(rem))
		out = append(out, msgs...)
		out = append(out, rem...)
		return out
	}
}

// RegisterPluginContributions wires declarative plugin entry projectors and
// context transforms into a registry. Plugin errors are fault-tolerant:
// invalid entries are skipped and warned, matching plugin.Discover semantics.
func RegisterPluginContributions(reg *Registry, mgr *plugin.Manager, warn io.Writer) {
	if reg == nil || mgr == nil {
		return
	}
	for _, p := range mgr.Plugins() {
		for customType, spec := range p.Manifest.EntryProjectors {
			fn := fixedProjector(customType, spec)
			if err := reg.RegisterEntryProjector(customType, fn); err != nil {
				if warn != nil {
					fmt.Fprintf(warn, "pigo: plugin %q entry projector %q: %v\n", p.Manifest.Name, customType, err)
				}
			}
		}
		for _, spec := range p.Manifest.ContextTransforms {
			content := spec.Content
			name := spec.Name
			if name == "" {
				name = "plugin:" + p.Manifest.Name
			}
			if err := reg.RegisterTransform(name, func(_ context.Context, msgs agentcore.MessageList) agentcore.MessageList {
				if content == "" {
					return msgs
				}
				out := make(agentcore.MessageList, 0, len(msgs)+1)
				out = append(out, msgs...)
				out = append(out, agentcore.UserMessage{
					RoleField: agentcore.RoleUser,
					Content:   agentcore.ContentList{agentcore.NewTextContent(content)},
				})
				return out
			}); err != nil {
				if warn != nil {
					fmt.Fprintf(warn, "pigo: plugin %q context transform %q: %v\n", p.Manifest.Name, name, err)
				}
			}
		}
	}
}

func fixedProjector(customType string, spec plugin.EntryProjectorSpec) EntryProjector {
	return func(entry session.V4Entry, _ int, _ []session.V4Entry) []agentcore.Message {
		content := spec.Content
		if entry.Content != "" && content == "" {
			content = entry.Content
		}
		return []agentcore.Message{agentcore.CustomMessage{
			RoleField:  agentcore.RoleCustom,
			CustomType: customType,
			Content:    agentcore.ContentList{agentcore.NewTextContent(content)},
			Display:    spec.Display,
			Timestamp:  entry.Timestamp.UnixMilli(),
		}}
	}
}
