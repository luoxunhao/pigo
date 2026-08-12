package httpapi

import (
	"fmt"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// ForkChoices lists the historical user messages for /fork selection.
func (s *SessionService) ForkChoices(sessionID, directory string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	entries, err := store.Entries(sessionID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, e := range entries {
		if e.Type != session.EntryTypeMessage {
			continue
		}
		msg, err := e.MessageValue()
		if err != nil {
			continue
		}
		u, ok := msg.(agentcore.UserMessage)
		if !ok {
			continue
		}
		n++
		text := strings.ReplaceAll(agentcore.ContentToText(u.Content), "\n", " ")
		fmt.Fprintf(&b, "%d. %s\n", n, text)
	}
	if n == 0 {
		return "no user messages to fork from", nil
	}
	return "fork from which message? run /fork <n>:\n" + b.String(), nil
}

// Fork creates a new session branching from before the n-th user message.
func (s *SessionService) Fork(sessionID, directory string, n int) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	entries, err := store.Entries(sessionID)
	if err != nil {
		return "", err
	}
	users := 0
	leafID := ""
	for _, e := range entries {
		if e.Type == session.EntryTypeMessage {
			msg, err := e.MessageValue()
			if err != nil {
				continue
			}
			if _, ok := msg.(agentcore.UserMessage); !ok {
				continue
			}
			users++
			if users == n {
				leafID = e.ParentID
				break
			}
		}
	}
	if leafID == "" && users < n {
		return "", fmt.Errorf("invalid selection %d (1..%d)", n, users)
	}
	return s.forkFrom(sessionID, directory, leafID)
}

// Clone duplicates the session at its current tip.
func (s *SessionService) Clone(sessionID, directory string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	leaf, err := store.MainLeaf(sessionID)
	if err != nil {
		return "", err
	}
	if leaf == "" {
		return "", fmt.Errorf("nothing to clone yet - send a message first")
	}
	return s.forkFrom(sessionID, directory, leaf)
}

func (s *SessionService) forkFrom(sourceID, directory, leafID string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	newHeader, path, err := store.ForkV4(sourceID, leafID, time.Now().UTC())
	if err != nil {
		return "", err
	}
	meta := sessionstore.NewMetadata(newHeader.ID, "Session", "pigo", newHeader.Model, directory)
	meta.ParentSessionID = sourceID
	meta.MessageCount = len(path)
	meta.LastActiveAt = newHeader.UpdatedAt
	if err := store.ImportV4Entries(meta, newHeader, path, nil); err != nil {
		return "", err
	}
	return newHeader.ID, nil
}

// Tree renders the session branch tree, optionally switching the active leaf.
func (s *SessionService) Tree(sessionID, directory string, n int) (string, error) {
	text, _, err := s.TreeStructured(sessionID, directory, n)
	return text, err
}

// TreeStructured renders the tree and returns the structured sessionTree
// snapshot when the request is valid.
func (s *SessionService) TreeStructured(sessionID, directory string, n int) (string, *gen.StructuredResult, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", nil, err
	}
	entries, err := store.Entries(sessionID)
	if err != nil {
		return "", nil, err
	}
	if len(entries) == 0 {
		return "session tree is empty - send a message first", nil, nil
	}
	leaf, err := store.MainLeaf(sessionID)
	if err != nil {
		return "", nil, err
	}
	lines := session.RenderTreeLinesV4(entries, leaf)
	if n == 0 {
		var b strings.Builder
		b.WriteString("session tree (run /tree <n> to switch the active branch):\n")
		for i, l := range lines {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, l.Text)
		}
		snapshot, err := s.treeSnapshot(store, sessionID, leaf)
		if err != nil {
			return "", nil, err
		}
		return b.String(), snapshot, nil
	}
	if n < 1 || n > len(lines) {
		return "", nil, fmt.Errorf("invalid selection %d (1..%d)", n, len(lines))
	}
	target := lines[n-1].Entry
	if target.ID == leaf {
		return "Already at this point", nil, nil
	}
	targetID := target.ID
	if err := store.MoveLane(sessionID, "main", &targetID); err != nil {
		return "", nil, err
	}
	path := session.PathToLeafV4(entries, target.ID)
	snapshot, err := s.treeSnapshot(store, sessionID, target.ID)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("switched to branch at node %d (%d messages) - next prompt continues from here", n, len(path)), snapshot, nil
}

func (s *SessionService) treeSnapshot(store *sessionstore.Store, sessionID, leafID string) (*gen.StructuredResult, error) {
	proj, err := store.Projection(sessionID, leafID)
	if err != nil {
		return nil, err
	}
	nodes := make([]map[string]any, 0, len(proj.Entries))
	active := map[string]bool{}
	for _, e := range session.PathToLeafV4(proj.Entries, leafID) {
		active[e.ID] = true
	}
	for _, e := range proj.Entries {
		kind := treeNodeKind(e)
		if kind == "" {
			continue
		}
		node := map[string]any{
			"id":        e.ID,
			"parentId":  nil,
			"kind":      kind,
			"summary":   session.SummaryV4(e),
			"timestamp": e.Timestamp.Format(time.RFC3339),
		}
		if e.ParentID != "" {
			node["parentId"] = e.ParentID
		}
		if label, ok := proj.Labels[e.ID]; ok {
			node["label"] = label
		}
		nodes = append(nodes, node)
	}
	var activePath []string
	for _, e := range proj.Entries {
		if active[e.ID] {
			activePath = append(activePath, e.ID)
		}
	}
	lanes := make([]map[string]any, 0, len(proj.Lanes))
	for _, l := range proj.Lanes {
		item := map[string]any{"lane": l.Lane}
		if l.LeafID != nil {
			item["leafId"] = *l.LeafID
		} else {
			item["leafId"] = nil
		}
		lanes = append(lanes, item)
	}
	data := map[string]any{
		"nodes":          nodes,
		"currentLeafId":  leafID,
		"currentLane":    proj.Lane,
		"activePathIds":  activePath,
		"labels":         proj.Labels,
		"lanes":          lanes,
	}
	if leafID == "" {
		data["currentLeafId"] = nil
		data["activePathIds"] = []string{}
	}
	return &gen.StructuredResult{Version: 1, Kind: "sessionTree", Data: data}, nil
}

// Label sets or clears a label fact for a tree line.
func (s *SessionService) Label(sessionID, directory string, n int, label string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	entries, err := store.Entries(sessionID)
	if err != nil {
		return "", err
	}
	leaf, err := store.MainLeaf(sessionID)
	if err != nil {
		return "", err
	}
	lines := session.RenderTreeLinesV4(entries, leaf)
	if n < 1 || n > len(lines) {
		return "", fmt.Errorf("invalid selection %d (1..%d)", n, len(lines))
	}
	target := lines[n-1].Entry
	if err := store.SetLabel(sessionID, target.ID, label); err != nil {
		return "", err
	}
	if label == "" {
		return fmt.Sprintf("cleared label on node %d", n), nil
	}
	return fmt.Sprintf("set label on node %d: %s", n, label), nil
}

func treeNodeKind(e session.V4Entry) string {
	switch e.Type {
	case session.EntryTypeMessage:
		if msg, err := e.MessageValue(); err == nil {
			return msg.Role()
		}
		return "message"
	case session.EntryTypeCompaction:
		return "compaction"
	case session.EntryTypeBranchSummary:
		return "branch_summary"
	case session.EntryTypeCustomMessage:
		return "custom_message"
	case session.EntryTypeCustom:
		return "custom"
	case session.EntryTypeModelChange:
		return "model_change"
	case session.EntryTypeThinkingChange:
		return "thinking_level_change"
	default:
		return ""
	}
}

// Export writes the session transcript to a file.
func (s *SessionService) Export(sessionID, directory, path string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	if path == "" {
		path = sessionID + ".jsonl"
	}
	n, err := store.Export(sessionID, path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("exported %d entries to %s", n, path), nil
}

// Import materializes a JSONL export as a new session.
func (s *SessionService) Import(directory, path string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	newHeader, entries, facts, err := store.ImportV4(path, time.Now().UTC())
	if err != nil {
		return "", err
	}
	meta := sessionstore.NewMetadata(newHeader.ID, "Session", "pigo", newHeader.Model, directory)
	meta.ParentSessionID = newHeader.ParentSession
	meta.LastActiveAt = newHeader.UpdatedAt
	if err := store.ImportV4Entries(meta, newHeader, entries, facts); err != nil {
		return "", err
	}
	return fmt.Sprintf("imported %d entries from %s -> session %s", len(entries), path, newHeader.ID), nil
}

// CopyLast returns the most recent assistant reply. A serve process cannot
// reach the client's clipboard, so it degrades to printing the text.
func (s *SessionService) CopyLast(sessionID, directory string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	_, _, msgs, err := store.Load(sessionID)
	if err != nil {
		return "", err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if a, ok := msgs[i].(agentcore.AssistantMessage); ok {
			if text := strings.TrimSpace(agentcore.ContentToText(a.Content)); text != "" {
				return "last reply:\n" + text, nil
			}
		}
	}
	return "", fmt.Errorf("nothing to copy - no assistant reply yet")
}

// ResumeList renders the saved sessions for a workspace as a numbered list.
func (s *SessionService) ResumeList(directory string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	metas, err := store.List()
	if err != nil {
		return "", err
	}
	metas = nonSubagentSessions(metas)
	if len(metas) == 0 {
		return "no saved sessions to resume", nil
	}
	var b strings.Builder
	b.WriteString("saved sessions (run /resume <n> to switch):\n")
	for i, meta := range metas {
		title := meta.SessionName
		if title == "" {
			title = meta.SessionID
		}
		fmt.Fprintf(&b, "  %d. %s (%s, messages: %d)\n", i+1, title, meta.LastActiveAt.Format("2006-01-02 15:04"), meta.MessageCount)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ResumeSelect picks the n-th saved session for a workspace.
func (s *SessionService) ResumeSelect(directory string, n int) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	metas, err := store.List()
	if err != nil {
		return "", err
	}
	metas = nonSubagentSessions(metas)
	if n < 1 || n > len(metas) {
		return "", fmt.Errorf("invalid selection %d (1..%d)", n, len(metas))
	}
	meta := metas[n-1]
	return fmt.Sprintf("selected session: %s (workspace: %s, model: %s, messages: %d)",
		meta.SessionID, meta.WorkspacePath, meta.ModelName, meta.MessageCount), nil
}

func nonSubagentSessions(metas []sessionstore.Metadata) []sessionstore.Metadata {
	filtered := make([]sessionstore.Metadata, 0, len(metas))
	for _, meta := range metas {
		if meta.SessionKind == sessionstore.SessionKindSubagent || strings.HasPrefix(meta.SessionID, "subagent-") {
			continue
		}
		filtered = append(filtered, meta)
	}
	return filtered
}
