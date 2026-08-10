// Package sessionstore implements project-scoped session persistence for
// pigo. Sessions are grouped under $PIGO_HOME/projects/<workspace-slug>/sessions/
// so each project keeps an isolated session list. The layout mirrors the facts
// of ash's session persistence (metadata + index + transcript) so a future
// desktop client can read the store directly.
package sessionstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
)

// SchemaVersion is the current metadata/index schema version. It is written
// into every metadata and index file and checked on read; an unknown higher
// version is a hard error so a newer store is never silently misread.
const SchemaVersion = 1

// maxSlugLen mirrors ash's project slug cap. Longer canonical paths keep a
// readable prefix plus a sha256 suffix so the slug stays stable and unique.
const maxSlugLen = 200

// Status is the lifecycle status of a persisted session.
type Status string

const (
	StatusActive    Status = "active"
	StatusArchived  Status = "archived"
	StatusCompleted Status = "completed"
)

// Metadata is the project-scoped metadata persisted per session. The JSON
// shape is camelCase, matching ash's SessionMetadata contract closely enough
// for a future desktop client to read it directly.
type Metadata struct {
	SchemaVersion    int             `json:"schemaVersion"`
	SessionID        string          `json:"sessionId"`
	SessionName      string          `json:"sessionName"`
	AgentType        string          `json:"agentType"`
	SessionKind      string          `json:"sessionKind,omitempty"`
	ModelName        string          `json:"modelName"`
	CreatedAt        time.Time       `json:"createdAt"`
	LastActiveAt     time.Time       `json:"lastActiveAt"`
	LastFinishedAt   *time.Time      `json:"lastFinishedAt,omitempty"`
	TurnCount        int             `json:"turnCount"`
	MessageCount     int             `json:"messageCount"`
	ToolCallCount    int             `json:"toolCallCount"`
	Status           Status          `json:"status"`
	Tags             []string        `json:"tags,omitempty"`
	WorkspacePath    string          `json:"workspacePath"`
	WorkspaceHost    string          `json:"workspaceHostname,omitempty"`
	CustomMetadata   json.RawMessage `json:"customMetadata,omitempty"`
	ParentSessionID  string          `json:"parentSessionId,omitempty"`
	ParentToolCallID string          `json:"parentToolCallId,omitempty"`
	SubagentType     string          `json:"subagentType,omitempty"`
}

// SessionKindSubagent marks a child session created by the task tool.
const SessionKindSubagent = "subagent"

// DefaultWorkspaceHost is the hostname recorded for local workspaces, matching
// ash's local workspace identity.
const DefaultWorkspaceHost = "localhost"

// NewMetadata builds metadata for a fresh session with zero counts.
func NewMetadata(sessionID, sessionName, agentType, modelName, workspacePath string) Metadata {
	now := time.Now().UTC()
	return Metadata{
		SchemaVersion: SchemaVersion,
		SessionID:     sessionID,
		SessionName:   sessionName,
		AgentType:     agentType,
		ModelName:     modelName,
		CreatedAt:     now,
		LastActiveAt:  now,
		Status:        StatusActive,
		WorkspacePath: workspacePath,
		WorkspaceHost: DefaultWorkspaceHost,
	}
}

// StoredMetadataFile is the on-disk envelope for a metadata file.
type StoredMetadataFile struct {
	SchemaVersion int `json:"schemaVersion"`
	Metadata      `json:",inline"`
}

// IndexFile is the project-level session index.
type IndexFile struct {
	SchemaVersion int        `json:"schemaVersion"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	Sessions      []Metadata `json:"sessions"`
}

// PigoHome returns $PIGO_HOME, falling back to ~/.pigo when unset.
func PigoHome() (string, error) {
	if dir := os.Getenv("PIGO_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sessionstore: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".pigo"), nil
}

// ProjectsRoot returns the root that holds one directory per workspace.
func ProjectsRoot(pigoHome string) string {
	return filepath.Join(pigoHome, "projects")
}

// WorkspaceSlug derives a stable directory-safe slug from a workspace path.
// It mirrors ash's project runtime slug: canonical path, ASCII alphanumerics
// lowercased, everything else replaced with '-', trimmed, capped with a
// sha256 suffix when too long.
func WorkspaceSlug(workspacePath string) string {
	if strings.TrimSpace(workspacePath) == "" {
		return "workspace"
	}
	canonical := workspacePath
	if abs, err := filepath.Abs(workspacePath); err == nil {
		canonical = abs
	}
	canonical = filepath.Clean(canonical)

	var b strings.Builder
	for _, r := range canonical {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "workspace"
	}
	if len(slug) <= maxSlugLen {
		return slug
	}
	sum := sha256.Sum256([]byte(canonical))
	suffix := hex.EncodeToString(sum[:])[:12]
	prefix := strings.TrimRight(slug[:maxSlugLen-len(suffix)-1], "-")
	return prefix + "-" + suffix
}

// SessionsDirForWorkspace returns the sessions directory for a workspace under
// the given pigo home.
func SessionsDirForWorkspace(pigoHome, workspacePath string) string {
	return filepath.Join(ProjectsRoot(pigoHome), WorkspaceSlug(workspacePath), "sessions")
}

// Store is a project-scoped session store. It owns the metadata files and the
// project index, and delegates transcript persistence to the legacy JSONL
// session format so resume/rewind/tree behavior stays unchanged.
type Store struct {
	sessionsDir string
	transcripts *session.Store
	mu          sync.Mutex
}

// OpenForWorkspace opens (creating if needed) the session store for a
// workspace under the given pigo home.
func OpenForWorkspace(pigoHome, workspacePath string) (*Store, error) {
	dir := SessionsDirForWorkspace(pigoHome, workspacePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionstore: create sessions dir: %w", err)
	}
	ts, err := session.NewStore(dir)
	if err != nil {
		return nil, err
	}
	return &Store{sessionsDir: dir, transcripts: ts}, nil
}

// Dir returns the sessions directory this store owns.
func (s *Store) Dir() string { return s.sessionsDir }

// TranscriptStore exposes the underlying JSONL store for callers that need the
// legacy append/branch primitives.
func (s *Store) TranscriptStore() *session.Store { return s.transcripts }

// Create writes a new session: metadata plus transcript. It fails when the
// session id is empty or a session with the same id already exists.
func (s *Store) Create(meta Metadata, header session.SessionHeader, messages agentcore.MessageList) error {
	if meta.SessionID == "" {
		return errors.New("sessionstore: session id must not be empty")
	}
	if header.ID == "" {
		header.ID = meta.SessionID
	}
	if header.ID != meta.SessionID {
		return fmt.Errorf("sessionstore: header id %q != metadata id %q", header.ID, meta.SessionID)
	}
	meta.SchemaVersion = SchemaVersion
	normalize(&meta)
	if _, err := s.LoadMetadata(meta.SessionID); err == nil {
		return fmt.Errorf("sessionstore: session %q already exists", meta.SessionID)
	}
	if err := s.writeMetadata(meta); err != nil {
		return err
	}
	if err := s.transcripts.Save(header, messages); err != nil {
		return fmt.Errorf("sessionstore: save transcript: %w", err)
	}
	return s.upsertIndex(meta)
}

// ImportEntries materializes an existing transcript (entries with their
// id/parentId tree preserved) as a session in this store: metadata + transcript
// + index. It is the migration primitive that brings a legacy flat session into
// the project-scoped store without regenerating entry ids, so fork/clone/tree
// behavior survives the move. It fails when a session with the same id already
// exists in this store.
func (s *Store) ImportEntries(meta Metadata, header session.SessionHeader, entries []session.Entry) error {
	if meta.SessionID == "" {
		return errors.New("sessionstore: session id must not be empty")
	}
	if header.ID == "" {
		header.ID = meta.SessionID
	}
	if header.ID != meta.SessionID {
		return fmt.Errorf("sessionstore: header id %q != metadata id %q", header.ID, meta.SessionID)
	}
	meta.SchemaVersion = SchemaVersion
	normalize(&meta)
	if _, err := s.LoadMetadata(meta.SessionID); err == nil {
		return fmt.Errorf("sessionstore: session %q already exists", meta.SessionID)
	}
	if err := s.writeMetadata(meta); err != nil {
		return err
	}
	if err := s.transcripts.SaveEntries(header, entries); err != nil {
		return fmt.Errorf("sessionstore: save transcript: %w", err)
	}
	return s.upsertIndex(meta)
}

// SaveMetadata updates a session's metadata and refreshes the index. It does
// not touch the transcript.
func (s *Store) SaveMetadata(meta Metadata) error {
	if meta.SessionID == "" {
		return errors.New("sessionstore: session id must not be empty")
	}
	meta.SchemaVersion = SchemaVersion
	normalize(&meta)
	if err := s.writeMetadata(meta); err != nil {
		return err
	}
	return s.upsertIndex(meta)
}

// UpdateHeader rewrites a session's transcript header while preserving all
// existing entries. It is used when session/load rebuilds the system prompt so
// the corrected header is persisted for later resumes.
func (s *Store) UpdateHeader(sessionID string, header session.SessionHeader) error {
	if header.ID == "" {
		header.ID = sessionID
	}
	if header.ID != sessionID {
		return fmt.Errorf("sessionstore: header id %q != session id %q", header.ID, sessionID)
	}
	_, entries, err := s.transcripts.LoadEntries(sessionID)
	if err != nil {
		return err
	}
	return s.transcripts.SaveEntries(header, entries)
}

// LoadMetadata reads a session's metadata.
func (s *Store) LoadMetadata(sessionID string) (Metadata, error) {
	var file StoredMetadataFile
	if err := readJSON(s.metadataPath(sessionID), &file); err != nil {
		return Metadata{}, err
	}
	if file.SchemaVersion > SchemaVersion {
		return Metadata{}, fmt.Errorf("sessionstore: metadata schema %d newer than supported %d", file.SchemaVersion, SchemaVersion)
	}
	return file.Metadata, nil
}

// Load reads a session's metadata plus its transcript header and messages.
func (s *Store) Load(sessionID string) (Metadata, session.SessionHeader, agentcore.MessageList, error) {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return Metadata{}, session.SessionHeader{}, nil, err
	}
	header, msgs, err := s.transcripts.Load(sessionID)
	if err != nil {
		return Metadata{}, session.SessionHeader{}, nil, err
	}
	return meta, header, msgs, nil
}

// Append grows an existing session's transcript and bumps its metadata counts
// and last-active timestamp. The session must already exist.
func (s *Store) Append(sessionID string, updatedAt time.Time, messages agentcore.MessageList) error {
	if len(messages) == 0 {
		return nil
	}
	if err := s.transcripts.Append(sessionID, updatedAt, messages); err != nil {
		return err
	}
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	meta.LastActiveAt = updatedAt
	meta.MessageCount += len(messages)
	for _, m := range messages {
		switch m.(type) {
		case agentcore.UserMessage:
			meta.TurnCount++
		case agentcore.ToolResultMessage:
			meta.ToolCallCount++
		}
	}
	return s.SaveMetadata(meta)
}

// AppendBranch grows an existing session's transcript as a branch descending
// from parentLeafID and refreshes the metadata counts/timestamp. It mirrors
// Append but preserves the on-disk entry tree (and therefore any sibling
// branches), so /tree leaf-switching and fork behavior keep working on the
// unified store. An empty parentLeafID roots a new chain.
func (s *Store) AppendBranch(sessionID string, header session.SessionHeader, parentLeafID string, messages agentcore.MessageList) (string, error) {
	if len(messages) == 0 {
		return parentLeafID, nil
	}
	leaf, err := s.transcripts.AppendBranch(header, parentLeafID, messages)
	if err != nil {
		return "", err
	}
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return "", err
	}
	if header.UpdatedAt.IsZero() {
		meta.LastActiveAt = time.Now().UTC()
	} else {
		meta.LastActiveAt = header.UpdatedAt
	}
	meta.MessageCount += len(messages)
	for _, m := range messages {
		switch m.(type) {
		case agentcore.UserMessage:
			meta.TurnCount++
		case agentcore.ToolResultMessage:
			meta.ToolCallCount++
		}
	}
	return leaf, s.SaveMetadata(meta)
}

// Touch refreshes a session's last-active timestamp.
func (s *Store) Touch(sessionID string) error {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	meta.LastActiveAt = time.Now().UTC()
	return s.SaveMetadata(meta)
}

// List returns all visible sessions, most recently active first. The index is
// used when present and consistent; otherwise it is rebuilt by scanning the
// metadata files.
func (s *Store) List() ([]Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// ListAll returns the metadata of every session in every project store under
// pigoHome, most recently active first. It is the cross-project listing used by
// --list-sessions / --continue and the dream distillation source. A missing
// projects root (no project-scoped sessions yet) is not an error; corrupt
// metadata files are skipped so one bad session cannot hide the rest.
func ListAll(pigoHome string) ([]Metadata, error) {
	root := ProjectsRoot(pigoHome)
	projects, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sessionstore: read projects root: %w", err)
	}
	var all []Metadata
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(root, p.Name(), "sessions")
		files, err := os.ReadDir(dir)
		if err != nil {
			continue // a project without a sessions dir is not an error
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".metadata.json") {
				continue
			}
			var file StoredMetadataFile
			if err := readJSON(filepath.Join(dir, f.Name()), &file); err != nil {
				continue
			}
			if file.SchemaVersion > SchemaVersion {
				continue
			}
			file.Metadata.SessionID = strings.TrimSuffix(f.Name(), ".metadata.json")
			all = append(all, file.Metadata)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].LastActiveAt.After(all[j].LastActiveAt)
	})
	return all, nil
}

// Delete removes a session's metadata and transcript and updates the index.
func (s *Store) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range []string{s.metadataPath(sessionID), s.transcriptPath(sessionID)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("sessionstore: remove %s: %w", p, err)
		}
	}
	index, err := s.loadIndexLocked()
	if err != nil {
		return err
	}
	kept := index.Sessions[:0]
	for _, m := range index.Sessions {
		if m.SessionID != sessionID {
			kept = append(kept, m)
		}
	}
	index.Sessions = kept
	index.UpdatedAt = time.Now().UTC()
	return s.writeIndexLocked(index)
}

// Helpers.

func normalize(meta *Metadata) {
	if meta.WorkspaceHost == "" {
		meta.WorkspaceHost = DefaultWorkspaceHost
	}
	if meta.Status == "" {
		meta.Status = StatusActive
	}
	if meta.Tags == nil {
		meta.Tags = []string{}
	}
}

func (s *Store) metadataPath(id string) string {
	return filepath.Join(s.sessionsDir, id+".metadata.json")
}

func (s *Store) transcriptPath(id string) string {
	return filepath.Join(s.sessionsDir, session.FileName(id))
}

func (s *Store) indexPath() string {
	return filepath.Join(s.sessionsDir, "index.json")
}

func (s *Store) writeMetadata(meta Metadata) error {
	file := StoredMetadataFile{SchemaVersion: SchemaVersion, Metadata: meta}
	return writeJSONAtomic(s.metadataPath(meta.SessionID), file)
}

func (s *Store) upsertIndex(meta Metadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndexLocked()
	if err != nil {
		return err
	}
	replaced := false
	for i := range index.Sessions {
		if index.Sessions[i].SessionID == meta.SessionID {
			index.Sessions[i] = meta
			replaced = true
			break
		}
	}
	if !replaced {
		index.Sessions = append(index.Sessions, meta)
	}
	sort.Slice(index.Sessions, func(i, j int) bool {
		return index.Sessions[i].LastActiveAt.After(index.Sessions[j].LastActiveAt)
	})
	index.UpdatedAt = time.Now().UTC()
	return s.writeIndexLocked(index)
}

func (s *Store) listLocked() ([]Metadata, error) {
	index, err := s.loadIndexLocked()
	if err == nil && index.SchemaVersion == SchemaVersion && indexConsistent(index.Sessions, s.sessionsDir) {
		return index.Sessions, nil
	}
	return s.rebuildIndexLocked()
}

func (s *Store) loadIndexLocked() (IndexFile, error) {
	var index IndexFile
	if err := readJSON(s.indexPath(), &index); err != nil {
		if os.IsNotExist(err) {
			return IndexFile{SchemaVersion: SchemaVersion}, nil
		}
		return IndexFile{}, err
	}
	return index, nil
}

func (s *Store) writeIndexLocked(index IndexFile) error {
	index.SchemaVersion = SchemaVersion
	return writeJSONAtomic(s.indexPath(), index)
}

func (s *Store) rebuildIndexLocked() ([]Metadata, error) {
	entries, err := os.ReadDir(s.sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: read sessions dir: %w", err)
	}
	var metas []Metadata
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".metadata.json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".metadata.json")
		meta, err := s.LoadMetadata(id)
		if err != nil {
			continue // one corrupt session must not hide the rest
		}
		metas = append(metas, meta)
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].LastActiveAt.After(metas[j].LastActiveAt)
	})
	index := IndexFile{
		SchemaVersion: SchemaVersion,
		UpdatedAt:     time.Now().UTC(),
		Sessions:      metas,
	}
	if err := s.writeIndexLocked(index); err != nil {
		return nil, err
	}
	return metas, nil
}

func indexConsistent(sessions []Metadata, dir string) bool {
	for _, m := range sessions {
		if _, err := os.Stat(filepath.Join(dir, m.SessionID+".metadata.json")); err != nil {
			return false
		}
	}
	return true
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("sessionstore: empty file %s", path)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("sessionstore: parse %s: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sessionstore: create dir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("sessionstore: marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("sessionstore: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("sessionstore: write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("sessionstore: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("sessionstore: rename %s: %w", path, err)
	}
	return nil
}
