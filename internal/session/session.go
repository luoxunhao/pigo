// Package session implements local JSONL session persistence and resume
// (US-024, #43). A session is stored as a single append-only JSONL file: the
// first line is a SessionHeader (schema version + metadata), and every
// subsequent line is one persisted message (user / assistant / toolResult),
// using the same "role"-discriminated encoding as agentcore.MessageList.
//
// The format is internally self-consistent and deliberately NOT wire-compatible
// with pi's session files (spec #16, session-format decision #5): pigo owns the schema
// and versions it via SessionHeader.Version so future migrations have a hook.
//
// A persisted session round-trips into an agentcore.AgentContext via Load, so a
// run can be resumed by feeding the reconstructed context to a fresh run and the
// transcript replays correctly in the REPL.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
)

// SchemaVersion is the current session file schema version. It is written into
// every SessionHeader and checked on read; an unknown (higher) version is a
// hard error so a newer file is never silently misread by an older binary.
//
// v2 adds the inline "compaction" message role (US-003): a compaction
// checkpoint persisted as one message line.
//
// v3 gives every persisted entry an id/parentId, forming a tree (US-005, #121)
// — the prerequisite for fork/clone/tree navigation. Each message line is
// wrapped as {"id","parentId","timestamp","message":{…}}. v1/v2 files (bare
// message lines) remain fully readable: readSession migrates them on load by
// synthesizing ids and chaining parentId to the previous entry, so old sessions
// still load and resume.
const SchemaVersion = 3

// sessionScanBufInit / sessionScanBufMax bound the line scanner used to read a
// session file. A single line holds one message, which can be large (a long
// tool result), so the max is raised well past bufio.Scanner's 64KiB default.
const (
	sessionScanBufInit = 64 * 1024
	sessionScanBufMax  = 16 * 1024 * 1024
)

// SessionHeader is the first line of a session file: schema version plus the
// metadata needed to list and resume a session without reading its messages.
type SessionHeader struct {
	// Version is the schema version (SchemaVersion at write time).
	Version int `json:"version"`
	// ID is the session identifier, also the file stem (see FileName).
	ID string `json:"id"`
	// CreatedAt is when the session file was created (RFC 3339, UTC).
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt is when the session was last appended to.
	UpdatedAt time.Time `json:"updatedAt"`
	// Model / Provider record what the session ran against, for display and to
	// re-establish the run configuration on resume. Optional.
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	// SystemPrompt is the system prompt the session ran under. Persisted so the
	// resumed context is faithful. Optional.
	SystemPrompt string `json:"systemPrompt,omitempty"`
	// ParentSession is the id of the session this one was forked/cloned from
	// (US-006, #122). Empty for a session created from scratch. It records
	// lineage only; a fork is otherwise a fully independent session file.
	ParentSession string `json:"parentSession,omitempty"`
	// Cwd is the absolute working directory the session ran in, recorded so a
	// session can be attributed to a project (its stable project id derives from
	// this path). Used by /dream distillation to select the current project's
	// recent sessions (SPEC §5.3 / §11.3). Optional and additive: older schemas
	// omit it and still load; a session with an empty Cwd is treated as
	// unattributed and never matches a project-scoped distill.
	Cwd string `json:"cwd,omitempty"`
	// LaneConfig is the authoritative lane.config register for the main lane.
	// It is written to the lane_config table on create/update and loaded back
	// into ProjectLeaf.Config by Store.Projection.
	LaneConfig *LaneConfig `json:"laneConfig,omitempty"`
	// ContextFrom is the id of the session this one inherited its collapsed
	// context from (#480, "infinite context"). Empty when the session started
	// with no inherited checkpoint. Optional and additive: older schemas (v1/v2/v3)
	// omit it and still load.
	ContextFrom string `json:"contextFrom,omitempty"`
	// ContextWatermark is the message index up to which context was collapsed
	// into an inherited checkpoint (#480). Messages before this index live in the
	// checkpoint summary rather than the replayed transcript. Optional/additive.
	ContextWatermark int `json:"contextWatermark,omitempty"`
}

// Entry wraps one persisted message with the tree metadata introduced in schema
// v3 (US-005, #121): a stable ID plus the ParentID it descends from. A linear
// session is the degenerate tree where every entry's ParentID is the previous
// entry's ID; the first entry has an empty ParentID (a root). The wrapper is
// what lets a session fork/clone later — PathToLeaf walks the ParentID chain to
// reconstruct the linear conversation feeding any leaf.
type Entry struct {
	// ID is this entry's stable identifier (see newEntryID). Unique within a file.
	ID string
	// ParentID is the ID this entry descends from; empty for a root entry.
	ParentID string
	// Timestamp is when the entry was persisted (RFC 3339, UTC).
	Timestamp time.Time
	// Message is the wrapped agent message (user / assistant / toolResult / …).
	Message agentcore.Message
}

// entryWire is the on-disk JSON shape of an Entry: the message is carried as a
// raw object so it round-trips through MessageList's role-discriminated decoder
// (agentcore.Message is a sealed interface with no default unmarshaler).
type entryWire struct {
	ID        string          `json:"id"`
	ParentID  string          `json:"parentId,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// MarshalJSON emits the entry as {"id","parentId","timestamp","message":{…}}.
func (e Entry) MarshalJSON() ([]byte, error) {
	mb, err := json.Marshal(e.Message)
	if err != nil {
		return nil, fmt.Errorf("session: encode entry message: %w", err)
	}
	return json.Marshal(entryWire{ID: e.ID, ParentID: e.ParentID, Timestamp: e.Timestamp, Message: mb})
}

// UnmarshalJSON decodes an entry line, decoding the inner message with the same
// discriminated logic as agentcore.MessageList (by wrapping it in a one-element
// array).
func (e *Entry) UnmarshalJSON(data []byte) error {
	var w entryWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	e.ID = w.ID
	e.ParentID = w.ParentID
	e.Timestamp = w.Timestamp
	if len(w.Message) == 0 {
		return fmt.Errorf("session: entry missing message")
	}
	var one agentcore.MessageList
	if err := json.Unmarshal([]byte("["+string(w.Message)+"]"), &one); err != nil {
		return fmt.Errorf("session: decode entry message: %w", err)
	}
	if len(one) != 1 {
		return fmt.Errorf("session: entry decoded to %d messages, want 1", len(one))
	}
	e.Message = one[0]
	return nil
}

// newEntryID returns a fresh 8-hex-character entry id (4 random bytes). This
// mirrors pi's generateEntryId (uuidv7().slice(-8)) in width; pigo does not need
// the time-ordering of uuidv7 because entry order is already given by the
// ParentID chain, so a simple random id suffices and collisions within a single
// file are astronomically unlikely.
func newEntryID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never fails in practice; fall back to a timestamp-derived
		// id so a session write never aborts on this path.
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// NewUUIDv7 returns a UUIDv7 session id, matching pi's session id convention.
func NewUUIDv7() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%06d", time.Now().UnixMilli(), time.Now().Nanosecond()/1000%1_000_000)
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewID returns a UUIDv7 session id. The time argument is retained for API
// compatibility; creation timestamps are stored separately.
func NewID(time.Time) string {
	return NewUUIDv7()
}


// NewEntryID returns a fresh 8-hex entry id.
func NewEntryID() string {
	return newEntryID()
}
