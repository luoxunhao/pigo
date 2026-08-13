package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
)

// V4SchemaVersion is the typed JSONL export/import schema version. It is not a
// runtime storage version; SQLite is canonical and v4 JSONL is only a portable
// export/import format.
const V4SchemaVersion = 4

// EntryType values mirror pi's nine typed session entries.
const (
	EntryTypeMessage            = "message"
	EntryTypeModelChange        = "model_change"
	EntryTypeThinkingChange     = "thinking_level_change"
	EntryTypeCompaction         = "compaction"
	EntryTypeBranchSummary      = "branch_summary"
	EntryTypeCustom             = "custom"
	EntryTypeLabel              = "label"
	EntryTypeSessionInfo        = "session_info"
)

// V4Header is the first line of a v4 JSONL export. pigo extension fields are
// omitempty so the format stays close to pi.
type V4Header struct {
	Type                 string    `json:"type"`
	Version              int       `json:"version"`
	ID                   string    `json:"id"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
	Cwd                  string    `json:"cwd"`
	Model                string    `json:"model,omitempty"`
	Provider             string    `json:"provider,omitempty"`
	SystemPrompt         string    `json:"systemPrompt,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
	ParentSessionID      string    `json:"parentSessionId,omitempty"`
	LeafID               *string   `json:"leafId,omitempty"`
	Lanes                []LaneState `json:"lanes,omitempty"`
}

// V4Entry is one typed session entry in v4 JSONL and in the SQLite payload.
// Outer id/parentId/timestamp match pi; type-specific fields are flattened so
// the export is human-readable and diff-friendly.
type V4Entry struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	ParentID       string          `json:"parentId,omitempty"`
	Timestamp      time.Time       `json:"timestamp"`
	Message        json.RawMessage `json:"message,omitempty"`
	CustomType     string          `json:"customType,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	Content        string          `json:"content,omitempty"`
	Display        any             `json:"display,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	RetainedTail   []json.RawMessage `json:"retainedTail,omitempty"`
	Details        json.RawMessage `json:"details,omitempty"`
	Usage          json.RawMessage `json:"usage,omitempty"`
	FromHook       bool            `json:"fromHook,omitempty"`
	Provider       string          `json:"provider,omitempty"`
	ModelID        string          `json:"modelId,omitempty"`
	ThinkingLevel  string          `json:"thinkingLevel,omitempty"`
	TargetID       string          `json:"targetId,omitempty"`
	Label          string          `json:"label,omitempty"`
	Name           string          `json:"name,omitempty"`
	TokensBefore   int             `json:"tokensBefore,omitempty"`
}

// V4Fact is a name/label fact row carried in the export after entries.
type V4Fact struct {
	Type  string `json:"type"`
	Kind  string `json:"kind"`
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

// IsMessageEntry reports whether the entry projects into agent context.
func (e V4Entry) IsMessageEntry() bool {
	switch e.Type {
	case EntryTypeMessage, EntryTypeBranchSummary, EntryTypeCompaction:
		return true
	default:
		return false
	}
}

// Message decodes the entry's agentcore message. It returns an error for
// metadata entries that never carry a message.
func (e V4Entry) MessageValue() (agentcore.Message, error) {
	if len(e.Message) == 0 {
		switch e.Type {
		case EntryTypeCompaction:
			return agentcore.CompactionMessage{
				RoleField:    agentcore.RoleCompaction,
				Summary:      e.Summary,
				TokensBefore: e.TokensBefore,
				Details:      e.Details,
				Timestamp:    e.Timestamp.UnixMilli(),
			}, nil
		case EntryTypeBranchSummary:
			return agentcore.BranchSummaryMessage{
				RoleField: agentcore.RoleBranchSummary,
				Summary:   e.Summary,
				FromID:    e.TargetID,
				Timestamp: e.Timestamp.UnixMilli(),
			}, nil
		default:
			return nil, fmt.Errorf("session: v4 entry %s has no message", e.ID)
		}
	}
	var one agentcore.MessageList
	if err := json.Unmarshal([]byte("["+string(e.Message)+"]"), &one); err != nil {
		return nil, fmt.Errorf("session: decode v4 message %s: %w", e.ID, err)
	}
	if len(one) != 1 {
		return nil, fmt.Errorf("session: v4 message %s decoded to %d messages, want 1", e.ID, len(one))
	}
	return one[0], nil
}

// NewV4Entry wraps an agentcore message as a typed message entry.
func NewV4Entry(id, parentID string, ts time.Time, msg agentcore.Message) (V4Entry, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return V4Entry{}, fmt.Errorf("session: encode v4 message: %w", err)
	}
	return V4Entry{Type: EntryTypeMessage, ID: id, ParentID: parentID, Timestamp: ts, Message: raw}, nil
}

// LaneState is the persisted lane/leaf pair used by projection and ACP meta.
type LaneState struct {
	Lane   string      `json:"lane"`
	LeafID *string     `json:"leafId,omitempty"`
	Config *LaneConfig `json:"config,omitempty"`
}

// ProjectLeaf is the unified root-to-leaf projection used by every front-end.
type ProjectLeaf struct {
	LeafID        string
	Lane          string
	Lanes         []LaneState
	Entries       []V4Entry
	Messages      agentcore.MessageList
	Model         string
	Provider      string
	ThinkingLevel string
	Labels        map[string]string
	Config        *LaneConfig
}

// V4TreeLine pairs one v4 entry with its rendered display line.
type V4TreeLine struct {
	Entry V4Entry
	Text  string
}

// RenderTreeLinesV4 renders the v4 entry forest as numbered text lines. The
// active leaf is tagged with "-> current" and metadata-only entries are shown
// with a compact label.
func RenderTreeLinesV4(entries []V4Entry, leafID string) []V4TreeLine {
	children := map[string][]V4Entry{}
	var roots []V4Entry
	for _, e := range entries {
		if e.ParentID == "" {
			roots = append(roots, e)
		} else {
			children[e.ParentID] = append(children[e.ParentID], e)
		}
	}
	sortEntries := func(list []V4Entry) {
		slices.SortStableFunc(list, func(a, b V4Entry) int {
			if !a.Timestamp.Equal(b.Timestamp) {
				if a.Timestamp.Before(b.Timestamp) {
					return -1
				}
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})
	}
	sortEntries(roots)
	for _, list := range children {
		sortEntries(list)
	}
	var out []V4TreeLine
	var walk func(e V4Entry, depth int)
	walk = func(e V4Entry, depth int) {
		if !treeLineVisible(e) {
			for _, c := range children[e.ID] {
				walk(c, depth)
			}
			return
		}
		marker := ""
		if e.ID == leafID {
			marker = " -> current"
		}
		text := strings.Repeat("  ", depth) + v4TreeKind(e) + ": " + v4TreeSummary(e) + marker
		out = append(out, V4TreeLine{Entry: e, Text: text})
		for _, c := range children[e.ID] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

func treeLineVisible(e V4Entry) bool {
	switch e.Type {
	case EntryTypeLabel, EntryTypeCustom, EntryTypeModelChange, EntryTypeThinkingChange, EntryTypeSessionInfo:
		return false
	case EntryTypeMessage:
		if msg, err := e.MessageValue(); err == nil {
			if a, ok := msg.(agentcore.AssistantMessage); ok {
				return strings.TrimSpace(agentcore.ContentToText(a.Content)) != ""
			}
		}
	default:
		return true
	}
	return true
}

func v4TreeKind(e V4Entry) string {
	switch e.Type {
	case EntryTypeMessage:
		if msg, err := e.MessageValue(); err == nil {
			return msg.Role()
		}
		return "message"
	case EntryTypeCompaction:
		return "compaction"
	case EntryTypeBranchSummary:
		return "branch_summary"
	case EntryTypeCustom:
		return "custom"
	case EntryTypeLabel:
		return "label"
	case EntryTypeSessionInfo:
		return "session_info"
	case EntryTypeModelChange:
		return "model_change"
	case EntryTypeThinkingChange:
		return "thinking_level_change"
	default:
		return e.Type
	}
}

func v4TreeSummary(e V4Entry) string {
	switch e.Type {
	case EntryTypeCompaction, EntryTypeBranchSummary:
		return oneLine(e.Summary)
	case EntryTypeModelChange:
		return e.ModelID
	case EntryTypeThinkingChange:
		return e.ThinkingLevel
	case EntryTypeLabel:
		return e.Label
	case EntryTypeSessionInfo:
		return e.Name
	case EntryTypeMessage:
		if msg, err := e.MessageValue(); err == nil {
			switch m := msg.(type) {
			case agentcore.UserMessage:
				return oneLine(agentcore.ContentToText(m.Content))
			case agentcore.AssistantMessage:
				return oneLine(agentcore.ContentToText(m.Content))
			case agentcore.ToolResultMessage:
				return oneLine(agentcore.ContentToText(m.Content))
			}
		}
	}
	return ""
}

// SummaryV4 returns the one-line summary used by structured tree nodes.
func SummaryV4(e V4Entry) string {
	return v4TreeSummary(e)
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " ..."
	}
	const max = 72
	if len(s) > max {
		s = s[:max] + " ..."
	}
	return s
}

// BuildProjection constructs the root-to-leaf path, applies the latest
// compaction retainedTail, and derives model/thinking state and labels.
func BuildProjection(entries []V4Entry, lanes []LaneState, leafID string, facts []V4Fact) (*ProjectLeaf, error) {
	path := PathToLeafV4(entries, leafID)
	labels := make(map[string]string)
	for _, f := range facts {
		if f.Kind == "label" && f.Key != "" && f.Value != "" {
			labels[f.Key] = f.Value
		}
	}
	leaf := &ProjectLeaf{
		LeafID:        leafID,
		Lane:          "main",
		Lanes:         lanes,
		Entries:       path,
		Labels:        labels,
		ThinkingLevel: "medium",
	}
	var mainConfig *LaneConfig
	for _, l := range lanes {
		if l.Lane == "main" {
			leaf.Lane = l.Lane
			mainConfig = l.Config
		}
	}
	if len(entries) > 0 && mainConfig == nil {
		return nil, fmt.Errorf("session: lane.config missing for main lane")
	}
	if mainConfig != nil {
		leaf.Config = mainConfig
		leaf.Model = mainConfig.Model
		leaf.Provider = mainConfig.Provider
		if mainConfig.ThinkingLevel != "" {
			leaf.ThinkingLevel = mainConfig.ThinkingLevel
		}
	}

	// Retained-tail projection: the newest compaction entry is self-contained.
	compactionIdx := -1
	for i, e := range path {
		if e.Type == EntryTypeCompaction {
			compactionIdx = i
		}
	}
	if compactionIdx >= 0 {
		projected := []V4Entry{path[compactionIdx]}
		for _, raw := range path[compactionIdx].RetainedTail {
			var msg agentcore.Message
			var one agentcore.MessageList
			if err := json.Unmarshal([]byte("["+string(raw)+"]"), &one); err == nil && len(one) == 1 {
				msg = one[0]
			}
			if msg == nil {
				continue
			}
			projected = append(projected, V4Entry{
				Type:      EntryTypeMessage,
				ID:        "retained-" + fmt.Sprint(len(projected)),
				ParentID:  path[compactionIdx].ID,
				Timestamp: path[compactionIdx].Timestamp,
				Message:   raw,
			})
		}
		if compactionIdx+1 < len(path) {
			projected = append(projected, path[compactionIdx+1:]...)
		}
		path = projected
	}
	leaf.Entries = path
	for _, e := range path {
		if !e.IsMessageEntry() {
			continue
		}
		msg, err := e.MessageValue()
		if err != nil {
			return nil, err
		}
		leaf.Messages = append(leaf.Messages, msg)
	}
	return leaf, nil
}

// PathToLeafV4 walks parent links from leafID to root.
func PathToLeafV4(entries []V4Entry, leafID string) []V4Entry {
	if leafID == "" {
		return nil
	}
	byID := make(map[string]V4Entry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	var rev []V4Entry
	seen := make(map[string]bool, len(entries))
	for id := leafID; id != ""; {
		e, ok := byID[id]
		if !ok || seen[id] {
			break
		}
		seen[id] = true
		rev = append(rev, e)
		id = e.ParentID
	}
	slices.Reverse(rev)
	return rev
}

// WriteV4JSONL writes a header, entries, and facts as v4 JSONL.
func WriteV4JSONL(w io.Writer, header V4Header, entries []V4Entry, facts []V4Fact) error {
	header.Type = "session"
	header.Version = V4SchemaVersion
	enc := json.NewEncoder(w)
	if err := enc.Encode(header); err != nil {
		return fmt.Errorf("session: encode v4 header: %w", err)
	}
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("session: encode v4 entry %s: %w", e.ID, err)
		}
	}
	for _, f := range facts {
		f.Type = "fact"
		if err := enc.Encode(f); err != nil {
			return fmt.Errorf("session: encode v4 fact: %w", err)
		}
	}
	return nil
}

// ReadV4JSONL parses a v4 JSONL stream. Any non-v4 header is rejected.
func ReadV4JSONL(r io.Reader) (V4Header, []V4Entry, []V4Fact, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, sessionScanBufInit), sessionScanBufMax)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return V4Header{}, nil, nil, err
		}
		return V4Header{}, nil, nil, errors.New("session: v4 import is empty")
	}
	var header V4Header
	if err := json.Unmarshal(sc.Bytes(), &header); err != nil {
		return V4Header{}, nil, nil, fmt.Errorf("session: parse v4 header: %w", err)
	}
	if header.Type != "session" || header.Version != V4SchemaVersion {
		return V4Header{}, nil, nil, fmt.Errorf("session: v4 import requires version %d, got %s v%d", V4SchemaVersion, header.Type, header.Version)
	}
	var entries []V4Entry
	var facts []V4Fact
	seen := make(map[string]bool)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return V4Header{}, nil, nil, fmt.Errorf("session: parse v4 line: %w", err)
		}
		if probe.Type == "fact" {
			var f V4Fact
			if err := json.Unmarshal(raw, &f); err != nil {
				return V4Header{}, nil, nil, err
			}
			facts = append(facts, f)
			continue
		}
		var e V4Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return V4Header{}, nil, nil, err
		}
		if e.ID == "" || seen[e.ID] {
			return V4Header{}, nil, nil, fmt.Errorf("session: v4 entry missing or duplicate id %q", e.ID)
		}
		seen[e.ID] = true
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return V4Header{}, nil, nil, err
	}
	// Validate parent references and physical ordering.
	seenBefore := make(map[string]bool)
	for _, e := range entries {
		if e.ParentID != "" && !seen[e.ParentID] {
			return V4Header{}, nil, nil, fmt.Errorf("session: v4 entry %s references unknown parent %q", e.ID, e.ParentID)
		}
		if e.ParentID != "" && !seenBefore[e.ParentID] {
			return V4Header{}, nil, nil, fmt.Errorf("session: v4 parent %q must precede child %q", e.ParentID, e.ID)
		}
		seenBefore[e.ID] = true
	}
	return header, entries, facts, nil
}

// ToLegacyEntries converts v4 entries to the legacy v3 Entry shape used by
// local front-ends that still render through the old tree API.
func ToLegacyEntries(v4 []V4Entry) ([]Entry, error) {
	out := make([]Entry, 0, len(v4))
	for _, e := range v4 {
		le := Entry{ID: e.ID, ParentID: e.ParentID, Timestamp: e.Timestamp}
		if e.IsMessageEntry() {
			msg, err := e.MessageValue()
			if err != nil {
				return nil, err
			}
			le.Message = msg
		}
		out = append(out, le)
	}
	return out, nil
}
