// Package sessionstore implements the canonical pigo session store. Sessions
// live in a single SQLite database at $PIGO_HOME/sessions.db; projects are
// distinguished by sessions.cwd. v4 typed JSONL is used only for export/import.
package sessionstore

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
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
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/session"
	_ "modernc.org/sqlite"
)

// SchemaVersion is kept for metadata compatibility. SQLite migrations own the
// canonical schema version now.
const SchemaVersion = 1

// maxSlugLen mirrors ash's project slug cap.
const maxSlugLen = 200

// Status is the lifecycle status of a persisted session.
type Status string

const (
	StatusActive    Status = "active"
	StatusArchived  Status = "archived"
	StatusCompleted Status = "completed"
)

// Metadata is the project-scoped metadata persisted per session. The JSON
// shape is camelCase for client compatibility. Header is pigo's runtime header
// (system prompt, model, timestamps, lineage) stored inside sessions.metadata.
type Metadata struct {
	SchemaVersion    int                    `json:"schemaVersion"`
	SessionID        string                 `json:"sessionId"`
	SessionName      string                 `json:"sessionName"`
	AgentType        string                 `json:"agentType"`
	SessionKind      string                 `json:"sessionKind,omitempty"`
	ModelName        string                 `json:"modelName"`
	CreatedAt        time.Time              `json:"createdAt"`
	LastActiveAt     time.Time              `json:"lastActiveAt"`
	LastFinishedAt   *time.Time             `json:"lastFinishedAt,omitempty"`
	TurnCount        int                    `json:"turnCount"`
	MessageCount     int                    `json:"messageCount"`
	ToolCallCount    int                    `json:"toolCallCount"`
	Status           Status                 `json:"status"`
	Tags             []string               `json:"tags,omitempty"`
	WorkspacePath    string                 `json:"workspacePath"`
	WorkspaceHost    string                 `json:"workspaceHostname,omitempty"`
	CustomMetadata   json.RawMessage        `json:"customMetadata,omitempty"`
	ParentSessionID  string                 `json:"parentSessionId,omitempty"`
	ParentToolCallID string                 `json:"parentToolCallId,omitempty"`
	SubagentType     string                 `json:"subagentType,omitempty"`
	Plugin           string                 `json:"plugin,omitempty"`
	Header           *session.SessionHeader `json:"header,omitempty"`
}

// SessionKindSubagent marks a child session.
const SessionKindSubagent = "subagent"

// DefaultWorkspaceHost is the hostname recorded for local workspaces.
const DefaultWorkspaceHost = "localhost"

//go:embed migrations/001_initial.sql
var migration001 string

//go:embed migrations/002_lane_config.sql
var migration002 string

var (
	dbMu sync.Mutex
	dbs  = map[string]*sql.DB{}
)

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

// WorkspaceSlug derives a stable directory-safe slug from a workspace path.
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

// DatabasePath returns the canonical SQLite database path.
func DatabasePath(pigoHome string) string {
	return filepath.Join(pigoHome, "sessions.db")
}

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

// Store is a project-scoped view over the canonical SQLite database.
type Store struct {
	db       *sql.DB
	pigoHome string
	cwd      string
	mu       sync.Mutex
}

// Open opens the canonical store for a pigo home.
func Open(pigoHome string) (*Store, error) {
	if pigoHome == "" {
		return nil, errors.New("sessionstore: pigoHome must not be empty")
	}
	db, err := openDB(pigoHome)
	if err != nil {
		return nil, err
	}
	st := &Store{db: db, pigoHome: pigoHome}
	return st, nil
}

// OpenForWorkspace opens the canonical store scoped to a workspace.
func OpenForWorkspace(pigoHome, workspacePath string) (*Store, error) {
	st, err := Open(pigoHome)
	if err != nil {
		return nil, err
	}
	if workspacePath != "" {
		st.cwd = filepath.Clean(workspacePath)
	}
	return st, nil
}

func openDB(pigoHome string) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db := dbs[pigoHome]; db != nil {
		return db, nil
	}
	if err := os.MkdirAll(pigoHome, 0o755); err != nil {
		return nil, fmt.Errorf("sessionstore: create pigo home: %w", err)
	}
	path := DatabasePath(pigoHome)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("sessionstore: %s: %w", pragma, err)
		}
	}
	if err := applyMigrations(db); err != nil {
		db.Close()
		return nil, err
	}
	dbs[pigoHome] = db
	return db, nil
}

func applyMigrations(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("sessionstore: create schema_migrations: %w", err)
	}
	var version int
	err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return fmt.Errorf("sessionstore: read migrations: %w", err)
	}
	migrations := []struct {
		version int
		sql     string
	}{
		{1, migration001},
		{2, migration002},
	}
	for _, m := range migrations {
		if version >= m.version {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("sessionstore: apply migration %03d: %w", m.version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, m.version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = m.version
	}
	return nil
}

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
	meta.SchemaVersion = SchemaVersion
}

func newOwnerID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("owner-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (s *Store) claimLeaseTx(tx *sql.Tx, sessionID string) (owner string, fence int64, err error) {
	owner = newOwnerID()
	now := time.Now().UnixMilli()
	expires := now + 30000
	res, err := tx.Exec(`INSERT INTO writer_leases(session_id, owner_id, fence, expires_at_ms)
		VALUES(?,?,1,?)
		ON CONFLICT(session_id) DO UPDATE SET
			owner_id=excluded.owner_id,
			fence=writer_leases.fence+1,
			expires_at_ms=excluded.expires_at_ms
		WHERE writer_leases.expires_at_ms <= ?`, sessionID, owner, expires, now)
	if err != nil {
		return "", 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", 0, err
	}
	if n == 0 {
		return "", 0, fmt.Errorf("sessionstore: writer lease was lost for %s", sessionID)
	}
	// Read the fence back (1 on first insert, incremented on takeover).
	err = tx.QueryRow(`SELECT fence FROM writer_leases WHERE session_id=? AND owner_id=?`, sessionID, owner).Scan(&fence)
	if err != nil {
		return "", 0, err
	}
	return owner, fence, nil
}

func (s *Store) releaseLeaseTx(tx *sql.Tx, sessionID, owner string, fence int64) {
	_, _ = tx.Exec(`DELETE FROM writer_leases WHERE session_id=? AND owner_id=? AND fence=?`, sessionID, owner, fence)
}

func (s *Store) withLease(sessionID string, fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner, fence, err := s.claimLeaseTx(tx, sessionID)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, _ = s.db.Exec(`DELETE FROM writer_leases WHERE session_id=? AND owner_id=? AND fence=?`, sessionID, owner, fence)
	return nil
}

// Close closes the underlying database handle and forgets it from the process
// registry. Tests and short-lived CLI processes should call it when done.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	dbMu.Lock()
	if dbs[s.pigoHome] == s.db {
		delete(dbs, s.pigoHome)
	}
	dbMu.Unlock()
	return s.db.Close()
}

// CloseAll closes every process-level SQLite handle. Tests use it to release
// temp databases before TempDir cleanup.
func CloseAll() {
	dbMu.Lock()
	for _, db := range dbs {
		_ = db.Close()
	}
	dbs = map[string]*sql.DB{}
	dbMu.Unlock()
}

func (s *Store) upsertSessionTx(tx *sql.Tx, meta Metadata, header session.SessionHeader) error {
	meta.SchemaVersion = SchemaVersion
	normalize(&meta)
	if header.ID == "" {
		header.ID = meta.SessionID
	}
	meta.Header = &header
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if header.CreatedAt.IsZero() {
		header.CreatedAt = time.Now().UTC()
	}
	if header.UpdatedAt.IsZero() {
		header.UpdatedAt = header.CreatedAt
	}
	cwd := header.Cwd
	if cwd == "" {
		cwd = meta.WorkspacePath
	}
	parent := meta.ParentSessionID
	if parent == "" {
		parent = header.ParentSession
	}
	_, err = tx.Exec(`INSERT INTO sessions(id, created_at, cwd, parent_session_id, metadata)
		VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			cwd=excluded.cwd,
			parent_session_id=excluded.parent_session_id,
			metadata=excluded.metadata`,
		header.ID, header.CreatedAt.UTC().Format(time.RFC3339), cwd, nullable(parent), string(raw))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_sequences(session_id, next_seq) VALUES(?,0)`, header.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_stats(session_id, message_count, cached_tokens, uncached_tokens, total_tokens, cost_total) VALUES(?,0,0,0,0,0)`, header.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(session_id, lane, leaf_id, open_operation_id) VALUES(?,'main',NULL,NULL)`, header.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO lane_config(session_id, lane, config) VALUES(?,'main','{}')`, header.ID); err != nil {
		return err
	}
	if header.LaneConfig != nil {
		raw, err := json.Marshal(header.LaneConfig)
		if err != nil {
			return fmt.Errorf("sessionstore: encode lane config: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO lane_config(session_id, lane, config) VALUES(?, 'main', ?)
			ON CONFLICT(session_id, lane) DO UPDATE SET config=excluded.config`, header.ID, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadMetadataRow(row *sql.Row) (Metadata, error) {
	var raw string
	err := row.Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, notFound("")
	}
	if err != nil {
		return Metadata{}, err
	}
	return decodeMetadata(raw)
}

func decodeMetadata(raw string) (Metadata, error) {
	var m Metadata
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Metadata{}, fmt.Errorf("sessionstore: decode metadata: %w", err)
	}
	normalize(&m)
	return m, nil
}

func notFound(id string) error {
	return fmt.Errorf("sessionstore: session %q not found: %w", id, os.ErrNotExist)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Create writes a new session (sessions/sequences/stats/main lane) and claims
// a writer lease in the same transaction.
func (s *Store) Create(meta Metadata, header session.SessionHeader, messages agentcore.MessageList) error {
	if meta.SessionID == "" && header.ID == "" {
		return errors.New("sessionstore: session id must not be empty")
	}
	id := meta.SessionID
	if id == "" {
		id = header.ID
	}
	if header.ID == "" {
		header.ID = id
	}
	if header.ID != id {
		return fmt.Errorf("sessionstore: header id %q != metadata id %q", header.ID, id)
	}
	meta.SessionID = id
	meta.Header = &header
	return s.withLease(id, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return fmt.Errorf("sessionstore: session %q already exists", id)
		}
		if err := s.upsertSessionTx(tx, meta, header); err != nil {
			return err
		}
		if len(messages) > 0 {
			if err := s.insertMessagesTx(tx, id, "", messages); err != nil {
				return err
			}
			meta.MessageCount = len(messages)
			meta.TurnCount, meta.ToolCallCount = countMessages(messages)
			raw, err := json.Marshal(meta)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`UPDATE sessions SET metadata=? WHERE id=?`, string(raw), id)
			return err
		}
		return nil
	})
}

func countMessages(msgs agentcore.MessageList) (turns, toolCalls int) {
	for _, m := range msgs {
		switch m.(type) {
		case agentcore.UserMessage:
			turns++
		case agentcore.ToolResultMessage:
			toolCalls++
		}
	}
	return turns, toolCalls
}

// titleFromUserText derives a compact display title from a user message.
// It uses the first non-empty line, trimmed and capped to 60 runes.
func titleFromUserText(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	const maxTitleRunes = 60
	r := []rune(text)
	if len(r) > maxTitleRunes {
		text = string(r[:maxTitleRunes]) + "..."
	}
	return text
}

// ImportV4Entries materializes v4 typed entries and facts into SQLite.
func (s *Store) ImportV4Entries(meta Metadata, header session.SessionHeader, entries []session.V4Entry, facts []session.V4Fact) error {
	if header.ID == "" {
		header.ID = meta.SessionID
	}
	if header.ID != meta.SessionID {
		return fmt.Errorf("sessionstore: header id %q != metadata id %q", header.ID, meta.SessionID)
	}
	meta.Header = &header
	return s.withLease(header.ID, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, header.ID).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return fmt.Errorf("sessionstore: session %q already exists", header.ID)
		}
		if err := s.upsertSessionTx(tx, meta, header); err != nil {
			return err
		}
		if err := s.insertV4EntriesTx(tx, header.ID, "", entries); err != nil {
			return err
		}
		return s.insertFactsTx(tx, header.ID, facts)
	})
}

// SaveMetadata updates a session's metadata JSON.
func (s *Store) SaveMetadata(meta Metadata) error {
	if meta.SessionID == "" {
		return errors.New("sessionstore: session id must not be empty")
	}
	normalize(&meta)
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return s.withLease(meta.SessionID, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET metadata=?, cwd=? WHERE id=?`, string(raw), meta.WorkspacePath, meta.SessionID)
		return err
	})
}

// LoadMetadata reads a session's metadata.
func (s *Store) LoadMetadata(sessionID string) (Metadata, error) {
	return s.loadMetadataRow(s.db.QueryRow(`SELECT metadata FROM sessions WHERE id=?`, sessionID))
}

// Load reads metadata, header, and the main-lane projection messages.
func (s *Store) Load(sessionID string) (Metadata, session.SessionHeader, agentcore.MessageList, error) {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return Metadata{}, session.SessionHeader{}, nil, err
	}
	var header session.SessionHeader
	if meta.Header != nil {
		header = *meta.Header
	}
	proj, err := s.Projection(sessionID, "")
	if err != nil {
		return Metadata{}, session.SessionHeader{}, nil, err
	}
	return meta, header, proj.Messages, nil
}

// UpdateHeader rewrites the stored header while preserving entries.
func (s *Store) UpdateHeader(sessionID string, header session.SessionHeader) error {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	header.ID = sessionID
	meta.Header = &header
	return s.SaveMetadata(meta)
}

// Append grows the session from the current main-lane leaf.
func (s *Store) Append(sessionID string, updatedAt time.Time, messages agentcore.MessageList) error {
	leaf, err := s.MainLeaf(sessionID)
	if err != nil {
		return err
	}
	header, err := s.Header(sessionID)
	if err != nil {
		return err
	}
	header.UpdatedAt = updatedAt
	_, err = s.AppendBranch(sessionID, header, leaf, messages)
	return err
}

// AppendBranch appends messages as a chain descending from parentLeafID.
func (s *Store) AppendBranch(sessionID string, header session.SessionHeader, parentLeafID string, messages agentcore.MessageList) (string, error) {
	if len(messages) == 0 {
		return parentLeafID, nil
	}
	if header.ID == "" {
		header.ID = sessionID
	}
	if header.ID != sessionID {
		return "", fmt.Errorf("sessionstore: header id %q != session id %q", header.ID, sessionID)
	}
	var entries []session.V4Entry
	for _, m := range messages {
		e, err := session.NewV4Entry(session.NewEntryID(), parentLeafID, time.Now().UTC(), m)
		if err != nil {
			return "", err
		}
		parentLeafID = e.ID
		entries = append(entries, e)
	}
	return s.appendV4Entries(sessionID, header, entries)
}

// AppendV4Entry appends one typed entry to the main lane.
func (s *Store) AppendV4Entry(sessionID string, header session.SessionHeader, e session.V4Entry) (string, error) {
	return s.appendV4Entries(sessionID, header, []session.V4Entry{e})
}

// AppendCompaction persists a compaction result as a typed compaction entry
// descending from the current main-lane leaf. Every front-end uses this single
// path so compaction survives process restarts and replays via retainedTail.
func (s *Store) AppendCompaction(sessionID string, header session.SessionHeader, res *compaction.CompactionResult) (string, error) {
	if res == nil {
		return s.MainLeaf(sessionID)
	}
	leaf, err := s.MainLeaf(sessionID)
	if err != nil {
		return "", err
	}
	header.ID = sessionID
	header.UpdatedAt = time.Now().UTC()
	return s.AppendV4Entry(sessionID, header, res.V4Entry(session.NewEntryID(), leaf, header.UpdatedAt))
}

func (s *Store) appendV4Entries(sessionID string, header session.SessionHeader, entries []session.V4Entry) (string, error) {
	if len(entries) == 0 {
		leaf, _ := s.MainLeaf(sessionID)
		return leaf, nil
	}
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		meta = NewMetadata(sessionID, "Session", "pigo", header.Model, header.Cwd)
		meta.Header = &header
	}
	if header.UpdatedAt.IsZero() {
		header.UpdatedAt = time.Now().UTC()
	}
	meta.Header = &header
	meta.ModelName = header.Model
	meta.LastActiveAt = header.UpdatedAt
	err = s.withLease(sessionID, func(tx *sql.Tx) error {
		exists := 0
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, sessionID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if err := s.upsertSessionTx(tx, meta, header); err != nil {
				return err
			}
		} else {
			raw, err := json.Marshal(meta)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE sessions SET metadata=?, cwd=?, parent_session_id=? WHERE id=?`,
				string(raw), meta.WorkspacePath, nullable(meta.ParentSessionID), sessionID); err != nil {
				return err
			}
		}
		if err := s.insertV4EntriesTx(tx, sessionID, "", entries); err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsMessageEntry() {
				if msg, err := e.MessageValue(); err == nil {
					switch msg.(type) {
					case agentcore.UserMessage:
						meta.TurnCount++
					case agentcore.ToolResultMessage:
						meta.ToolCallCount++
					}
				}
			}
		}
		messageCount := 0
		for _, e := range entries {
			if e.IsMessageEntry() {
				messageCount++
			}
		}
		meta.MessageCount += messageCount
		raw, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE sessions SET metadata=? WHERE id=?`, string(raw), sessionID)
		return err
	})
	if err != nil {
		return "", err
	}
	return entries[len(entries)-1].ID, nil
}

func (s *Store) insertMessagesTx(tx *sql.Tx, sessionID, parentLeafID string, messages agentcore.MessageList) error {
	var entries []session.V4Entry
	for _, m := range messages {
		e, err := session.NewV4Entry(session.NewEntryID(), parentLeafID, time.Now().UTC(), m)
		if err != nil {
			return err
		}
		parentLeafID = e.ID
		entries = append(entries, e)
	}
	return s.insertV4EntriesTx(tx, sessionID, "", entries)
}

func (s *Store) insertV4EntriesTx(tx *sql.Tx, sessionID, lane string, entries []session.V4Entry) error {
	if lane == "" {
		lane = "main"
	}
	var next int
	if err := tx.QueryRow(`SELECT next_seq FROM session_sequences WHERE session_id=?`, sessionID).Scan(&next); err != nil {
		return err
	}
	leaf := ""
	if err := tx.QueryRow(`SELECT COALESCE(leaf_id,'') FROM lanes WHERE session_id=? AND lane=?`, sessionID, lane).Scan(&leaf); err != nil {
		return err
	}
	for i := range entries {
		if entries[i].ID == "" {
			entries[i].ID = session.NewEntryID()
		}
		if entries[i].ParentID == "" && leaf != "" && i == 0 {
			entries[i].ParentID = leaf
		}
		if entries[i].Timestamp.IsZero() {
			entries[i].Timestamp = time.Now().UTC()
		}
		payload, err := json.Marshal(entries[i])
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO entries(session_id, seq, id, parent_id, type, timestamp, payload)
			VALUES(?,?,?,?,?,?,?)`,
			sessionID, next, entries[i].ID, nullable(entries[i].ParentID), entries[i].Type,
			entries[i].Timestamp.UTC().Format(time.RFC3339), string(payload)); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET leaf_id=? WHERE session_id=? AND lane=?`, entries[i].ID, sessionID, lane); err != nil {
			return err
		}
		leaf = entries[i].ID
		next++
	}
	if _, err := tx.Exec(`UPDATE session_sequences SET next_seq=? WHERE session_id=?`, next, sessionID); err != nil {
		return err
	}
	messageEntries := 0
	for _, e := range entries {
		if e.IsMessageEntry() {
			messageEntries++
		}
	}
	if messageEntries > 0 {
		if _, err := tx.Exec(`UPDATE session_stats SET message_count = message_count + ? WHERE session_id=?`, messageEntries, sessionID); err != nil {
			return err
		}
	}
	return s.rebuildBranchCacheTx(tx, sessionID)
}

func (s *Store) rebuildBranchCacheTx(tx *sql.Tx, sessionID string) error {
	if _, err := tx.Exec(`DELETE FROM branch_entries WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM branch_tips WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id, parent_id FROM entries WHERE session_id=? ORDER BY seq`, sessionID)
	if err != nil {
		return err
	}
	var nodes []session.V4Entry
	children := map[string][]string{}
	byID := map[string]session.V4Entry{}
	for rows.Next() {
		var n session.V4Entry
		var parent sql.NullString
		var id string
		if err := rows.Scan(&id, &parent); err != nil {
			rows.Close()
			return err
		}
		n.ID, n.ParentID = id, parent.String
		nodes = append(nodes, n)
		byID[id] = n
		if parent.Valid && parent.String != "" {
			children[parent.String] = append(children[parent.String], id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(byID) == 0 {
		return nil
	}
	var tips []string
	for _, n := range byID {
		if len(children[n.ID]) == 0 {
			tips = append(tips, n.ID)
		}
	}
	sort.Strings(tips)
	for _, tip := range tips {
		branchID := "branch-" + session.NewUUIDv7()
		path := session.PathToLeafV4(nodes, tip)
		for i, e := range path {
			if _, err := tx.Exec(`INSERT INTO branch_entries(session_id, branch_id, entry_id, entry_seq, entry_type, custom_type)
				VALUES(?,?,?,?,?,?)`, sessionID, branchID, e.ID, i, e.Type, nullable(e.CustomType)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO branch_tips(session_id, tip_id, branch_id) VALUES(?,?,?)`, sessionID, tip, branchID); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a session and its dependencies.
func (s *Store) Delete(sessionID string) error {
	return s.withLease(sessionID, func(tx *sql.Tx) error {
		for _, table := range []string{"branch_entries", "branch_tips", "facts", "lanes", "lane_moves", "records", "entries", "writer_leases", "session_stats", "session_sequences"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE session_id=?`, sessionID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE id=?`, sessionID); err != nil {
			return err
		}
		return nil
	})
}

// List returns visible sessions for this workspace, newest first.
func (s *Store) List() ([]Metadata, error) {
	rows, err := s.db.Query(`SELECT metadata FROM sessions WHERE cwd=?`, s.cwd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Metadata
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		m, err := decodeMetadata(raw)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	s.enrichDefaultTitles(out)
	return out, rows.Err()
}

// ListAll returns metadata of every session under pigoHome.
func ListAll(pigoHome string) ([]Metadata, error) {
	st, err := Open(pigoHome)
	if err != nil {
		return nil, err
	}
	rows, err := st.db.Query(`SELECT metadata FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Metadata
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		m, err := decodeMetadata(raw)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	st.enrichDefaultTitles(out)
	return out, rows.Err()
}

// enrichDefaultTitles replaces the placeholder "Session" title with the first
// user message when available, so session lists show a useful title without
// requiring an explicit /name. It only changes the in-memory metadata returned
// by List/ListAll; the persisted sessionName is updated on the next append.
func (s *Store) enrichDefaultTitles(metas []Metadata) {
	for i := range metas {
		if metas[i].SessionName != "Session" && metas[i].SessionName != "" {
			continue
		}
		title, err := s.firstUserTitle(metas[i].SessionID)
		if err == nil && title != "" {
			metas[i].SessionName = title
		}
	}
}

func (s *Store) firstUserTitle(sessionID string) (string, error) {
	var payload string
	err := s.db.QueryRow(`SELECT payload FROM entries
		WHERE session_id=? AND type='message' AND json_extract(payload,'$.message.role')='user'
		ORDER BY seq LIMIT 1`, sessionID).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	var e session.V4Entry
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return "", err
	}
	msg, err := e.MessageValue()
	if err != nil {
		return "", err
	}
	u, ok := msg.(agentcore.UserMessage)
	if !ok {
		return "", nil
	}
	return titleFromUserText(agentcore.ContentToText(u.Content)), nil
}

// Touch refreshes last-active.
func (s *Store) Touch(sessionID string) error {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	meta.LastActiveAt = time.Now().UTC()
	return s.SaveMetadata(meta)
}

// Entries returns all v4 entries in physical order.
func (s *Store) Entries(sessionID string) ([]session.V4Entry, error) {
	rows, err := s.db.Query(`SELECT id, parent_id, type, timestamp, payload FROM entries WHERE session_id=? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntryRows(rows)
}

// Facts returns name/label facts.
func (s *Store) Facts(sessionID string) ([]session.V4Fact, error) {
	rows, err := s.db.Query(`SELECT kind, key, value FROM facts WHERE session_id=? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []session.V4Fact
	for rows.Next() {
		var kind string
		var key, value sql.NullString
		if err := rows.Scan(&kind, &key, &value); err != nil {
			return nil, err
		}
		out = append(out, session.V4Fact{Type: "fact", Kind: kind, Key: key.String, Value: value.String})
	}
	return out, rows.Err()
}

// Lanes returns the session's lane states.
func (s *Store) Lanes(sessionID string) ([]session.LaneState, error) {
	rows, err := s.db.Query(`SELECT l.lane, l.leaf_id, COALESCE(lc.config,'') FROM lanes l
		LEFT JOIN lane_config lc ON lc.session_id=l.session_id AND lc.lane=l.lane
		WHERE l.session_id=? ORDER BY CASE l.lane WHEN 'main' THEN 0 ELSE 1 END, l.lane`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []session.LaneState
	for rows.Next() {
		var lane string
		var leaf sql.NullString
		var config string
		if err := rows.Scan(&lane, &leaf, &config); err != nil {
			return nil, err
		}
		ls := session.LaneState{Lane: lane}
		if leaf.Valid {
			ls.LeafID = &leaf.String
		}
		if config != "" {
			var cfg session.LaneConfig
			if json.Unmarshal([]byte(config), &cfg) == nil {
				ls.Config = &cfg
			}
		}
		out = append(out, ls)
	}
	return out, rows.Err()
}

// SetLaneConfig upserts the authoritative config for a lane.
func (s *Store) SetLaneConfig(sessionID, lane string, cfg session.LaneConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("sessionstore: encode lane config: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO lane_config(session_id, lane, config) VALUES(?,?,?)
		ON CONFLICT(session_id, lane) DO UPDATE SET config=excluded.config`, sessionID, lane, string(raw))
	return err
}

// MainLeaf returns the main lane's leaf id.
func (s *Store) MainLeaf(sessionID string) (string, error) {
	var leaf sql.NullString
	if err := s.db.QueryRow(`SELECT leaf_id FROM lanes WHERE session_id=? AND lane='main'`, sessionID).Scan(&leaf); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", notFound(sessionID)
		}
		return "", err
	}
	return leaf.String, nil
}

// Header returns the stored session header.
func (s *Store) Header(sessionID string) (session.SessionHeader, error) {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return session.SessionHeader{}, err
	}
	if meta.Header != nil {
		return *meta.Header, nil
	}
	return session.SessionHeader{ID: sessionID, CreatedAt: meta.CreatedAt, UpdatedAt: meta.LastActiveAt, Model: meta.ModelName, Cwd: meta.WorkspacePath}, nil
}

// ProjectionWindow is a bounded slice of the main-lane projection. Total and
// Start let callers build cursors without materializing the full session.
type ProjectionWindow struct {
	Entries []session.V4Entry
	Total   int
	Start   int
	Lane    string
	LeafID  string
	Lanes   []session.LaneState
}

// branchWindow returns a page from the branch cache for the current main leaf.
// It reports whether the fast path was used.
func (s *Store) branchWindow(sessionID, leafID string, end, limit int) (*ProjectionWindow, bool, error) {
	branchID, err := s.branchIDForTip(sessionID, leafID)
	if err != nil {
		return nil, false, err
	}
	if branchID == "" {
		return nil, false, nil
	}
	total := 0
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM branch_entries WHERE session_id=? AND branch_id=?`, sessionID, branchID).Scan(&total); err != nil {
		return nil, false, err
	}
	start, end := windowBounds(total, end, limit)
	entries, err := s.queryBranchEntries(sessionID, branchID, start, end)
	if err != nil {
		return nil, false, err
	}
	lanes, err := s.Lanes(sessionID)
	if err != nil {
		return nil, false, err
	}
	return &ProjectionWindow{
		Entries: entries,
		Total:   total,
		Start:   start,
		Lane:    "main",
		LeafID:  leafID,
		Lanes:   lanes,
	}, true, nil
}

func (s *Store) branchIDForTip(sessionID, leafID string) (string, error) {
	var branchID string
	err := s.db.QueryRow(`SELECT branch_id FROM branch_tips WHERE session_id=? AND tip_id=?`, sessionID, leafID).Scan(&branchID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return branchID, err
}

// ProjectionWindow returns the main-lane projection window ending at end
// (entry index, inclusive-exclusive) with at most limit entries. end <= 0 means
// the end of the projection. The branch cache is used when safe so a long
// session is never fully loaded for one page; sessions with compaction fall
// back to the full projection path.
func (s *Store) ProjectionWindow(sessionID string, end, limit int) (*ProjectionWindow, error) {
	lanes, err := s.Lanes(sessionID)
	if err != nil {
		return nil, err
	}
	var leafID string
	for _, l := range lanes {
		if l.Lane == "main" && l.LeafID != nil {
			leafID = *l.LeafID
			break
		}
	}
	if limit <= 0 {
		limit = 50
	}

	if leafID != "" {
		branchID, err := s.branchIDForTip(sessionID, leafID)
		if err != nil {
			return nil, err
		}
		if branchID != "" {
			var hasCompaction int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM branch_entries b
				JOIN entries e ON e.session_id=b.session_id AND e.id=b.entry_id
				WHERE b.session_id=? AND b.branch_id=? AND e.type='compaction'`, sessionID, branchID).Scan(&hasCompaction); err != nil {
				return nil, err
			}
			if hasCompaction == 0 {
				win, ok, err := s.branchWindow(sessionID, leafID, end, limit)
				if err != nil {
					return nil, err
				}
				if ok {
					return win, nil
				}
			}
		}
	}

	proj, err := s.Projection(sessionID, "")
	if err != nil {
		return nil, err
	}
	start, end := windowBounds(len(proj.Entries), end, limit)
	return &ProjectionWindow{
		Entries: proj.Entries[start:end],
		Total:   len(proj.Entries),
		Start:   start,
		Lane:    proj.Lane,
		LeafID:  proj.LeafID,
		Lanes:   proj.Lanes,
	}, nil
}

// HistoryWindow returns a page of the full raw main-lane path, ignoring
// compaction projection. This is the client-facing history used by ACP replay:
// compaction may shorten the LLM context, but the UI must not lose earlier
// messages. The branch cache keeps the page bounded to O(limit).
func (s *Store) HistoryWindow(sessionID string, end, limit int) (*ProjectionWindow, error) {
	lanes, err := s.Lanes(sessionID)
	if err != nil {
		return nil, err
	}
	var leafID string
	for _, l := range lanes {
		if l.Lane == "main" && l.LeafID != nil {
			leafID = *l.LeafID
			break
		}
	}
	if limit <= 0 {
		limit = 50
	}
	if leafID != "" {
		win, ok, err := s.branchWindow(sessionID, leafID, end, limit)
		if err != nil {
			return nil, err
		}
		if ok {
			return win, nil
		}
	}

	entries, err := s.Entries(sessionID)
	if err != nil {
		return nil, err
	}
	path := session.PathToLeafV4(entries, leafID)
	start, end := windowBounds(len(path), end, limit)
	return &ProjectionWindow{
		Entries: path[start:end],
		Total:   len(path),
		Start:   start,
		Lane:    "main",
		LeafID:  leafID,
		Lanes:   lanes,
	}, nil
}

func windowBounds(total, end, limit int) (start, endOut int) {
	if end <= 0 || end > total {
		end = total
	}
	start = end - limit
	if start < 0 {
		start = 0
	}
	return start, end
}

func (s *Store) queryBranchEntries(sessionID, branchID string, start, end int) ([]session.V4Entry, error) {
	rows, err := s.db.Query(`SELECT e.id, e.parent_id, e.type, e.timestamp, e.payload
		FROM branch_entries b JOIN entries e ON e.session_id=b.session_id AND e.id=b.entry_id
		WHERE b.session_id=? AND b.branch_id=? AND b.entry_seq>=? AND b.entry_seq<?
		ORDER BY b.entry_seq`, sessionID, branchID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntryRows(rows)
}

func scanEntryRows(rows *sql.Rows) ([]session.V4Entry, error) {
	var out []session.V4Entry
	for rows.Next() {
		var id, typ, timestamp, payload string
		var parent sql.NullString
		if err := rows.Scan(&id, &parent, &typ, &timestamp, &payload); err != nil {
			return nil, err
		}
		var e session.V4Entry
		if err := json.Unmarshal([]byte(payload), &e); err != nil {
			return nil, err
		}
		e.ID = id
		e.Type = typ
		e.ParentID = parent.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// Projection builds the unified root-to-leaf projection.
func (s *Store) Projection(sessionID, leafID string) (*session.ProjectLeaf, error) {
	entries, err := s.Entries(sessionID)
	if err != nil {
		return nil, err
	}
	lanes, err := s.Lanes(sessionID)
	if err != nil {
		return nil, err
	}
	facts, err := s.Facts(sessionID)
	if err != nil {
		return nil, err
	}
	if leafID == "" {
		for _, l := range lanes {
			if l.Lane == "main" && l.LeafID != nil {
				leafID = *l.LeafID
			}
		}
	}
	if len(entries) > 0 && leafID == "" {
		return nil, fmt.Errorf("sessionstore: session %s main lane has no leaf", sessionID)
	}
	if leafID != "" {
		found := false
		for _, e := range entries {
			if e.ID == leafID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("sessionstore: leaf %q not found in session %s", leafID, sessionID)
		}
	}
	return session.BuildProjection(entries, lanes, leafID, facts)
}

// MoveLane persists a lane move and writes lane_moves.
func (s *Store) MoveLane(sessionID, lane string, leafID *string) error {
	return s.withLease(sessionID, func(tx *sql.Tx) error {
		if leafID != nil {
			var exists int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM entries WHERE session_id=? AND id=?`, sessionID, *leafID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return fmt.Errorf("sessionstore: entry %q not found", *leafID)
			}
		}
		if _, err := tx.Exec(`UPDATE lanes SET leaf_id=? WHERE session_id=? AND lane=?`, nullable(leafValue(leafID)), sessionID, lane); err != nil {
			return err
		}
		var next int
		if err := tx.QueryRow(`SELECT next_seq FROM session_sequences WHERE session_id=?`, sessionID).Scan(&next); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lane_moves(session_id, seq, lane, leaf_id) VALUES(?,?,?,?)`,
			sessionID, next, lane, nullable(leafValue(leafID))); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE session_sequences SET next_seq=? WHERE session_id=?`, next+1, sessionID)
		return err
	})
}

func leafValue(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

// SetName writes a name fact.
func (s *Store) SetName(sessionID, name string) error {
	return s.SetFact(sessionID, "name", "", name)
}

// SetLabel writes or clears a label fact.
func (s *Store) SetLabel(sessionID, targetID string, label string) error {
	return s.SetFact(sessionID, "label", targetID, label)
}

func (s *Store) SetFact(sessionID, kind, key, value string) error {
	return s.withLease(sessionID, func(tx *sql.Tx) error {
		if value == "" {
			if _, err := tx.Exec(`DELETE FROM facts WHERE session_id=? AND kind=? AND key IS ?`,
				sessionID, kind, nullable(key)); err != nil {
				return err
			}
		}
		return s.insertFactTx(tx, sessionID, kind, key, value)
	})
}

func (s *Store) insertFactsTx(tx *sql.Tx, sessionID string, facts []session.V4Fact) error {
	for _, f := range facts {
		if err := s.insertFactTx(tx, sessionID, f.Kind, f.Key, f.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) insertFactTx(tx *sql.Tx, sessionID, kind, key, value string) error {
	var next int
	if err := tx.QueryRow(`SELECT next_seq FROM session_sequences WHERE session_id=?`, sessionID).Scan(&next); err != nil {
		return err
	}
	var v any
	if value != "" {
		v = value
	}
	if _, err := tx.Exec(`INSERT INTO facts(session_id, seq, kind, key, value) VALUES(?,?,?,?,?)`,
		sessionID, next, kind, nullable(key), v); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE session_sequences SET next_seq=? WHERE session_id=?`, next+1, sessionID)
	return err
}

// Search runs a cwd-scoped FTS search.
func (s *Store) Search(query string, limit int) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT session_id, entry_id, kind, snippet(session_search_fts, 4, '[', ']', '...', 12)
		FROM session_search_fts
		WHERE session_id IN (SELECT id FROM sessions WHERE cwd=?) AND session_search_fts MATCH ?
		ORDER BY bm25(session_search_fts)
		LIMIT ?`, s.cwd, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.SessionID, &r.EntryID, &r.Kind, &r.Snippet); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchResult is one FTS hit.
type SearchResult struct {
	SessionID string `json:"sessionId"`
	EntryID   string `json:"entryId"`
	Kind      string `json:"kind"`
	Snippet   string `json:"snippet"`
}

func (s *Store) Save(header session.SessionHeader, messages agentcore.MessageList) error {
	meta, err := s.LoadMetadata(header.ID)
	if errors.Is(err, os.ErrNotExist) {
		meta = NewMetadata(header.ID, "Session", "pigo", header.Model, header.Cwd)
	}
	meta.Header = &header
	meta.MessageCount = len(messages)
	meta.TurnCount = 0
	meta.ToolCallCount = 0
	for _, m := range messages {
		switch m.(type) {
		case agentcore.UserMessage:
			meta.TurnCount++
		case agentcore.ToolResultMessage:
			meta.ToolCallCount++
		}
	}
	meta.LastActiveAt = header.UpdatedAt
	return s.withLease(header.ID, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM entries WHERE session_id=?`, header.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM facts WHERE session_id=?`, header.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET leaf_id=NULL WHERE session_id=?`, header.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE session_stats SET message_count=0, total_tokens=0 WHERE session_id=?`, header.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE session_sequences SET next_seq=0 WHERE session_id=?`, header.ID); err != nil {
			return err
		}
		if exists := 0; tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, header.ID).Scan(&exists) == nil && exists == 0 {
			if err := s.upsertSessionTx(tx, meta, header); err != nil {
				return err
			}
		} else {
			raw, err := json.Marshal(meta)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE sessions SET metadata=?, cwd=? WHERE id=?`, string(raw), header.Cwd, header.ID); err != nil {
				return err
			}
		}
		if len(messages) > 0 {
			if err := s.insertMessagesTx(tx, header.ID, "", messages); err != nil {
				return err
			}
		}
		return nil
	})
}

// ForkV4 creates a new session whose contents are the root-to-leaf path in the
// source session, copied verbatim as typed v4 entries. The new session gets a
// fresh uuidv7 id and records sourceID as its parent.
func (s *Store) ForkV4(sourceID, leafID string, now time.Time) (session.SessionHeader, []session.V4Entry, error) {
	entries, err := s.Entries(sourceID)
	if err != nil {
		return session.SessionHeader{}, nil, err
	}
	path := session.PathToLeafV4(entries, leafID)
	src, err := s.Header(sourceID)
	if err != nil {
		return session.SessionHeader{}, nil, err
	}
	newHeader := session.SessionHeader{
		ID:            session.NewID(now),
		CreatedAt:     now,
		UpdatedAt:     now,
		Model:         src.Model,
		Provider:      src.Provider,
		SystemPrompt:  src.SystemPrompt,
		ParentSession: sourceID,
		Cwd:           src.Cwd,
	}
	if lanes, err := s.Lanes(sourceID); err == nil {
		for _, l := range lanes {
			if l.Lane == "main" && l.Config != nil {
				cfg := *l.Config
				newHeader.LaneConfig = &cfg
				break
			}
		}
	}
	return newHeader, path, nil
}

func (s *Store) Export(id, outPath string) (int, error) {
	header, err := s.Header(id)
	if err != nil {
		return 0, err
	}
	entries, err := s.Entries(id)
	if err != nil {
		return 0, err
	}
	facts, err := s.Facts(id)
	if err != nil {
		return 0, err
	}
	leaf, _ := s.MainLeaf(id)
	v4Header := session.V4Header{
		Type:            "session",
		Version:         session.V4SchemaVersion,
		ID:              id,
		CreatedAt:       header.CreatedAt,
		UpdatedAt:       header.UpdatedAt,
		Cwd:             header.Cwd,
		Model:           header.Model,
		Provider:        header.Provider,
		SystemPrompt:    header.SystemPrompt,
		ParentSessionID: header.ParentSession,
	}
	if leaf != "" {
		v4Header.LeafID = &leaf
	}
	if lanes, err := s.Lanes(id); err == nil {
		v4Header.Lanes = lanes
	}
	f, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(outPath))
	if ext == ".html" || ext == ".htm" {
		legacy, err := session.ToLegacyEntries(entries)
		if err != nil {
			return 0, err
		}
		if err := session.WriteHTML(f, header, legacy); err != nil {
			return 0, err
		}
	} else {
		if err := session.WriteV4JSONL(f, v4Header, entries, facts); err != nil {
			return 0, err
		}
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// ImportV4 reads a v4 JSONL export and returns a fresh header, typed entries,
// and facts without writing them.
func (s *Store) ImportV4(inPath string, now time.Time) (session.SessionHeader, []session.V4Entry, []session.V4Fact, error) {
	f, err := os.Open(inPath)
	if err != nil {
		return session.SessionHeader{}, nil, nil, err
	}
	defer f.Close()
	src, entries, facts, err := session.ReadV4JSONL(f)
	if err != nil {
		return session.SessionHeader{}, nil, nil, err
	}
	newHeader := session.SessionHeader{
		ID:            session.NewID(now),
		CreatedAt:     now,
		UpdatedAt:     now,
		Model:         src.Model,
		Provider:      src.Provider,
		SystemPrompt:  src.SystemPrompt,
		ParentSession: src.ID,
		Cwd:           src.Cwd,
	}
	for _, lane := range src.Lanes {
		if lane.Lane == "main" && lane.Config != nil {
			cfg := *lane.Config
			newHeader.LaneConfig = &cfg
			break
		}
	}
	return newHeader, entries, facts, nil
}
