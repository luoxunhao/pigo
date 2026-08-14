// This file defines LiveConfig, the mutable run configuration a control command
// may change mid-session. It was moved verbatim from cmd/pigo (the former
// liveRunConfig) and exported so the run, repl, btw, status and goal
// subpackages can read and mutate it through the Host contract. The run closure
// reads it on every prompt, so a /model switch takes effect on the next turn.
// It carries no lock: it is read and written only on the REPL's single main
// goroutine (slash actions and the run are both invoked synchronously from the
// REPL loop, never concurrently).
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// LiveConfig is the mutable run configuration a control command may change
// mid-session.
type LiveConfig struct {
	Model        string
	ProviderName string
	Provider     provider.Provider
	BaseURL      string
	Protocol     string
	// PersistConfig, when non-nil, writes the current lane.config back to the
	// session store after a live-state command changes model/thinking. It keeps
	// lane.config authoritative across frontends.
	PersistConfig func()
	// Creds is the session credential store. A /model switch updates it with
	// the configured entry's API key so the next turn uses that model's own
	// credential. It may be nil in session-less construction paths.
	Creds *provider.CredentialStore
	// ThinkingLevel is the reasoning-effort level applied to each turn. It is
	// seeded from the resolved config chain and read on every prompt.
	ThinkingLevel agentcore.ThinkingLevel
	// ContextWindow is the model's total context-token budget, used to gate
	// automatic compaction. When 0 the window is unknown and auto-compaction is
	// disabled; the REPL seeds it with a conservative default so long sessions
	// still compact rather than overflow.
	ContextWindow int
}

// DefaultContextWindow is the fallback context-token budget used when a model's
// true window is unknown. It is deliberately large so auto-compaction only fires
// on genuinely long sessions (threshold = window - ReserveTokens), never on
// ordinary short exchanges.
const DefaultContextWindow = 128000

// PersistLaneConfig writes the live model/provider/thinking state back to the
// session's lane.config, preserving any existing active-tool selection.
func PersistLaneConfig(store *sessionstore.Store, sessionID string, live *LiveConfig) error {
	lanes, err := store.Lanes(sessionID)
	if err != nil {
		return err
	}
	cfg := &session.LaneConfig{
		Model:         live.Model,
		Provider:      live.ProviderName,
		ThinkingLevel: string(live.ThinkingLevel),
	}
	for _, l := range lanes {
		if l.Lane == "main" && l.Config != nil {
			cp := *l.Config
			cp.Model = live.Model
			cp.Provider = live.ProviderName
			cp.ThinkingLevel = string(live.ThinkingLevel)
			cfg = &cp
			break
		}
	}
	return store.SetLaneConfig(sessionID, "main", *cfg)
}

// ApplyProjectionToLive seeds a live config from the persisted lane.config and
// resolves the provider object for that model. It is used when resuming a
// session so lane.config, not CLI defaults, drives the run.
func ApplyProjectionToLive(live *LiveConfig, proj *session.ProjectLeaf, baseURL, protocol, apiKey string) error {
	if proj == nil || proj.Config == nil {
		return errors.New("cli: lane.config missing on resume")
	}
	cfg := proj.Config
	if cfg.Model == "" {
		return errors.New("cli: lane.config model is empty on resume")
	}
	oldModel, oldProviderName := live.Model, live.ProviderName
	live.Model = cfg.Model
	if cfg.Provider != "" {
		live.ProviderName = cfg.Provider
	}
	if cfg.ThinkingLevel != "" {
		live.ThinkingLevel = agentcore.ThinkingLevel(cfg.ThinkingLevel)
	}
	if live.Provider != nil && (cfg.Model != oldModel || (cfg.Provider != "" && cfg.Provider != oldProviderName)) {
		prov, name, resolvedKey, _, err := provider.ResolveConfiguredModel(cfg.Model, baseURL, protocol, cfg.Provider, apiKey, os.Getenv)
		if err != nil {
			return fmt.Errorf("cli: resolve lane.config model %q: %w", cfg.Model, err)
		}
		live.Provider = prov
		live.ProviderName = name
		if live.Creds != nil {
			live.Creds.ClearOverride(oldProviderName)
			live.Creds.SetOverride(name, resolvedKey)
		}
	}
	return nil
}
