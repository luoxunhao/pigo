package httpapi

import (
	"fmt"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// ForkChoices lists the historical user messages for /fork selection.
func (s *SessionService) ForkChoices(sessionID, directory string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	_, entries, err := store.TranscriptStore().LoadEntries(sessionID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, e := range entries {
		u, ok := e.Message.(agentcore.UserMessage)
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
	_, entries, err := store.TranscriptStore().LoadEntries(sessionID)
	if err != nil {
		return "", err
	}
	users := 0
	leafID := ""
	for _, e := range entries {
		if _, ok := e.Message.(agentcore.UserMessage); ok {
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
	_, entries, err := store.TranscriptStore().LoadEntries(sessionID)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("nothing to clone yet - send a message first")
	}
	return s.forkFrom(sessionID, directory, entries[len(entries)-1].ID)
}

func (s *SessionService) forkFrom(sourceID, directory, leafID string) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	newHeader, path, err := store.TranscriptStore().Fork(sourceID, leafID, time.Now().UTC())
	if err != nil {
		return "", err
	}
	meta := sessionstore.NewMetadata(newHeader.ID, "Session", "pigo", newHeader.Model, directory)
	meta.ParentSessionID = sourceID
	meta.MessageCount = len(path)
	meta.LastActiveAt = newHeader.UpdatedAt
	if err := store.ImportEntries(meta, newHeader, path); err != nil {
		return "", err
	}
	return newHeader.ID, nil
}

// Tree renders the session branch tree, optionally switching the active leaf.
func (s *SessionService) Tree(sessionID, directory string, n int) (string, error) {
	store, err := s.storeFor(directory)
	if err != nil {
		return "", err
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return "", err
	}
	_, entries, err := store.TranscriptStore().LoadEntries(sessionID)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "session tree is empty - send a message first", nil
	}
	custom := readSessionCustom(meta)
	curLeaf, _ := custom["curLeaf"].(string)
	if curLeaf == "" {
		curLeaf = entries[len(entries)-1].ID
	}
	lines := session.RenderTreeLines(entries, curLeaf)
	if n == 0 {
		var b strings.Builder
		b.WriteString("session tree (run /tree <n> to switch the active branch):\n")
		for i, l := range lines {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, l.Text)
		}
		return b.String(), nil
	}
	if n < 1 || n > len(lines) {
		return "", fmt.Errorf("invalid selection %d (1..%d)", n, len(lines))
	}
	target := lines[n-1].Entry
	custom["curLeaf"] = target.ID
	meta.CustomMetadata = writeSessionCustom(custom)
	if err := store.SaveMetadata(meta); err != nil {
		return "", err
	}
	path := session.PathToLeaf(entries, target.ID)
	return fmt.Sprintf("switched to branch at node %d (%d messages) - next prompt continues from here", n, len(path)), nil
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
	n, err := store.TranscriptStore().Export(sessionID, path)
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
	newHeader, entries, err := store.TranscriptStore().Import(path, time.Now().UTC())
	if err != nil {
		return "", err
	}
	meta := sessionstore.NewMetadata(newHeader.ID, "Session", "pigo", newHeader.Model, directory)
	meta.ParentSessionID = newHeader.ParentSession
	meta.MessageCount = len(entries)
	meta.LastActiveAt = newHeader.UpdatedAt
	if err := store.ImportEntries(meta, newHeader, entries); err != nil {
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
