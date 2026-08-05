// Package headless drives pigo's non-interactive run paths: the print /
// stream-json headless run, the session listing/resume helpers, and the
// process-isolated sub-agent JSON-RPC server (--subagent-rpc).
//
// Session persistence is unified on the project-scoped store: new sessions are
// written only under $PIGO_HOME/projects/<workspace-slug>/sessions, never to
// the legacy flat ~/.pigo/sessions directory. The legacy flat store stays
// readable as a fallback so old sessions can be listed and, on first resume,
// migrated once into the project-scoped store. This removes the previous
// double-write where interactive and headless paths persisted the same session
// to two different roots.
package headless

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// ProjectStore returns the project-scoped session store for the current working
// directory. It is the single write target for new session data. An
// unresolvable cwd falls back to the implicit "workspace" project bucket.
func ProjectStore() (*sessionstore.Store, error) {
	home, err := sessionstore.PigoHome()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return sessionstore.OpenForWorkspace(home, cwd)
}

// LegacySessionStore returns the legacy flat session store at
// $PIGO_HOME/sessions (else ~/.pigo/sessions). It is read-only for new data:
// sessions written before the project-scoped store remain readable, and a
// resumed legacy session is migrated into the project store on first use.
func LegacySessionStore() (*session.Store, error) {
	home, err := sessionstore.PigoHome()
	if err != nil {
		return nil, err
	}
	return session.NewStore(filepath.Join(home, "sessions"))
}

// SessionStore is retained as an alias for legacy callers. New callers should
// use ProjectStore; the flat store stays readable for old sessions.
func SessionStore() (*session.Store, error) {
	return LegacySessionStore()
}

// EnsureProjectSession makes sure sessionID exists in the project-scoped store
// for the given workspace. If it only exists in the legacy flat store, it is
// migrated once (metadata + transcript + index) so every subsequent write goes
// to the single project-scoped location. It returns an error when the session
// exists nowhere.
func EnsureProjectSession(home, cwd, sessionID string) error {
	proj, err := sessionstore.OpenForWorkspace(home, cwd)
	if err != nil {
		return err
	}
	if _, err := proj.LoadMetadata(sessionID); err == nil {
		return nil
	}
	migrated, err := migrateLegacySession(proj, sessionID, cwd)
	if err != nil {
		return err
	}
	if !migrated {
		return fmt.Errorf("pigo: session %q not found", sessionID)
	}
	return nil
}

// migrateLegacySession copies a legacy flat session into proj verbatim (entry
// ids/parentIds preserved) and re-anchors it to the resumed workspace so the
// store layout and metadata agree. It reports false when the legacy store has
// no such session.
func migrateLegacySession(proj *sessionstore.Store, sessionID, cwd string) (bool, error) {
	legacy, err := LegacySessionStore()
	if err != nil {
		return false, err
	}
	header, entries, err := legacy.LoadEntries(sessionID)
	if err != nil {
		return false, nil
	}
	header.Cwd = cwd
	meta := sessionstore.NewMetadata(sessionID, "Session", "pigo", header.Model, cwd)
	meta.CreatedAt = header.CreatedAt
	meta.LastActiveAt = header.UpdatedAt
	meta.MessageCount = len(entries)
	if err := proj.ImportEntries(meta, header, entries); err != nil {
		return false, err
	}
	return true, nil
}

// AllSessionHeaders returns headers for every known session across all project
// stores plus any not-yet-migrated legacy flat sessions, most recently updated
// first. Project-scoped entries win when an id exists in both.
func AllSessionHeaders() ([]session.SessionHeader, error) {
	home, err := sessionstore.PigoHome()
	if err != nil {
		return nil, err
	}
	metas, err := sessionstore.ListAll(home)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]session.SessionHeader, len(metas)+8)
	for _, m := range metas {
		byID[m.SessionID] = session.SessionHeader{
			ID:        m.SessionID,
			UpdatedAt: m.LastActiveAt,
			Model:     m.ModelName,
			Cwd:       m.WorkspacePath,
		}
	}
	if legacy, err := LegacySessionStore(); err == nil {
		if headers, err := legacy.List(); err == nil {
			for _, h := range headers {
				if _, ok := byID[h.ID]; !ok {
					byID[h.ID] = h
				}
			}
		}
	}
	out := make([]session.SessionHeader, 0, len(byID))
	for _, h := range byID {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// PrintSessions prints the stored sessions, most-recent first, to out.
func PrintSessions(out io.Writer) error {
	headers, err := AllSessionHeaders()
	if err != nil {
		return err
	}
	if len(headers) == 0 {
		fmt.Fprintln(out, "no sessions")
		return nil
	}
	for _, h := range headers {
		fmt.Fprintf(out, "%s\t%s\t%s\n", h.ID, h.UpdatedAt.Local().Format("2006-01-02 15:04"), h.Model)
	}
	return nil
}

// MostRecentSessionID returns the id of the most recently updated session, or
// "" if there are none.
func MostRecentSessionID() (string, error) {
	headers, err := AllSessionHeaders()
	if err != nil {
		return "", err
	}
	if len(headers) == 0 {
		return "", nil
	}
	return headers[0].ID, nil
}

// headlessSession is the session state backing one headless run: the
// project-scoped store, the header (whose ID is the session id emitted and used
// for resume), and the branch-tracking cursor (curLeaf/persisted) so the run's
// messages append as a branch descending from the resumed leaf rather than
// flattening the tree.
type headlessSession struct {
	store   *sessionstore.Store
	header  session.SessionHeader
	curLeaf string // active leaf id to descend from; "" for a fresh session
	// persisted is the number of agentCtx.Messages already on disk before the
	// run; persist appends only Messages[persisted:] as a new branch.
	persisted int
	// model/provider are the model and provider the run actually used, refreshed
	// onto the header before persisting so a resumed run does not write back the
	// original session's stale values.
	model    string
	provider string
}

// openHeadlessSession resolves the session backing a headless run: it resumes an
// existing session when resumeID is set (seeding priorMsgs and re-anchoring the
// branch leaf) or creates a fresh session otherwise. It returns the prior
// messages to seed into the context ahead of the new prompt, plus the session
// state used to persist the run afterward. Fresh sessions are created in the
// project-scoped store; a legacy flat session is migrated there on first resume.
func openHeadlessSession(resumeID, model, providerName, sysPrompt string) (agentcore.MessageList, headlessSession, error) {
	proj, err := ProjectStore()
	if err != nil {
		return nil, headlessSession{}, err
	}
	now := time.Now().UTC()
	cwd := headlessCwd()

	if resumeID != "" {
		_, h, msgs, err := proj.Load(resumeID)
		if err != nil {
			// Not in the project store yet: migrate the legacy flat session once,
			// then load from the single project-scoped location.
			if _, merr := migrateLegacySession(proj, resumeID, cwd); merr != nil {
				return nil, headlessSession{}, merr
			}
			_, h, msgs, err = proj.Load(resumeID)
			if err != nil {
				return nil, headlessSession{}, err
			}
		}
		curLeaf := ""
		if _, entries, lerr := proj.TranscriptStore().LoadEntries(resumeID); lerr == nil && len(entries) > 0 {
			curLeaf = entries[len(entries)-1].ID
		}
		// A resumed header keeps its own SystemPrompt when present so the run is
		// faithful to the original session.
		if h.SystemPrompt == "" {
			h.SystemPrompt = sysPrompt
		}
		return msgs, headlessSession{store: proj, header: h, curLeaf: curLeaf, persisted: len(msgs), model: model, provider: providerName}, nil
	}

	header := session.SessionHeader{
		ID:           session.NewID(now),
		CreatedAt:    now,
		UpdatedAt:    now,
		Model:        model,
		Provider:     providerName,
		SystemPrompt: sysPrompt,
		Cwd:          cwd,
	}
	meta := sessionstore.NewMetadata(header.ID, "Session", "pigo", model, cwd)
	if err := proj.Create(meta, header, nil); err != nil {
		return nil, headlessSession{}, err
	}
	return nil, headlessSession{store: proj, header: header, curLeaf: "", persisted: 0, model: model, provider: providerName}, nil
}

// headlessCwd returns the absolute working directory the run executes in, used
// to attribute the session to a project (SessionHeader.Cwd → project id) so a
// later /dream pass can distill this session under the right project scope. An
// unresolvable cwd yields "" (the session stays unattributed) rather than
// aborting the run.
func headlessCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// persist appends the messages produced during the run — everything in
// agentCtx.Messages past what was already on disk — as a branch descending from
// the resumed leaf in the project-scoped store, matching how the REPL grows a
// session tree (AppendBranch). It is a no-op when the run produced nothing new.
// Errors are returned for the caller to surface; the run's output has already
// been emitted regardless.
func (hs *headlessSession) persist(agentCtx *agentcore.AgentContext) error {
	// Compaction can rebuild agentCtx.Messages to fewer entries than were on disk
	// before the run (loop.go maybeAutoCompact replaces the slice). Clamp the
	// cursor so the tail slice stays in bounds; when the context shrank there is
	// nothing new to append past what compaction kept.
	if hs.persisted > len(agentCtx.Messages) {
		hs.persisted = len(agentCtx.Messages)
	}
	tail := agentCtx.Messages[hs.persisted:]
	if len(tail) == 0 {
		return nil
	}
	// Refresh the header with the model/provider the run actually used so a
	// resumed session's metadata is not written back stale.
	hs.header.Model = hs.model
	hs.header.Provider = hs.provider
	hs.header.UpdatedAt = time.Now().UTC()
	leaf, err := hs.store.AppendBranch(hs.header.ID, hs.header, hs.curLeaf, tail)
	if err != nil {
		return err
	}
	hs.curLeaf = leaf
	hs.persisted = len(agentCtx.Messages)
	return nil
}
