package sessionstore

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	ws := filepath.Join(home, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, ws
}

func TestMigrationsCreateCanonicalSchema(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "s1", CreatedAt: now, UpdatedAt: now, Cwd: ws}
	if err := st.Create(NewMetadata("s1", "S", "pigo", "m", ws), header, nil); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := st.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("migration version = %d, want 3", version)
	}
	var tables int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_search_fts'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatal("session_search_fts virtual table missing")
	}
}

func TestMigration003RemovesLegacyLaneConfigFields(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", DatabasePath(home))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, migration := range []string{migration001, migration002} {
		if _, err := db.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?), (2, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	metadata := `{"sessionId":"s1","modelName":"m","customMetadata":{"thinkingLevel":"high","mode":"build"},"header":{"id":"s1","laneConfig":{"model":"m","thinkingLevel":"medium"}}}`
	if _, err := db.Exec(`INSERT INTO sessions(id, created_at, cwd, metadata) VALUES('s1', ?, ?, ?)`, now, ws, metadata); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var raw string
	if err := st.db.QueryRow(`SELECT metadata FROM sessions WHERE id='s1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatal(err)
	}
	custom, _ := meta["customMetadata"].(map[string]any)
	if _, ok := custom["thinkingLevel"]; ok {
		t.Fatalf("customMetadata.thinkingLevel survived migration: %s", raw)
	}
	if _, ok := custom["mode"]; !ok {
		t.Fatalf("customMetadata.mode was removed by migration: %s", raw)
	}
	header, _ := meta["header"].(map[string]any)
	if _, ok := header["laneConfig"]; ok {
		t.Fatalf("header.laneConfig survived migration: %s", raw)
	}
}

func TestMoveLanePersistsLeafAndAudit(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "move", CreatedAt: now, UpdatedAt: now, Cwd: ws}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("a")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("b")}},
	}
	if err := st.Create(NewMetadata("move", "M", "pigo", "m", ws), header, msgs); err != nil {
		t.Fatal(err)
	}
	leaf, err := st.MainLeaf("move")
	if err != nil || leaf == "" {
		t.Fatalf("MainLeaf = %q, %v", leaf, err)
	}
	first := ""
	entries, err := st.Entries("move")
	if err != nil {
		t.Fatal(err)
	}
	first = entries[0].ID
	if err := st.MoveLane("move", "main", &first); err != nil {
		t.Fatal(err)
	}
	got, err := st.MainLeaf("move")
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("main leaf = %q, want %q", got, first)
	}
	var moves int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM lane_moves WHERE session_id='move'`).Scan(&moves); err != nil {
		t.Fatal(err)
	}
	if moves == 0 {
		t.Fatal("lane_moves audit row missing")
	}
}

func TestDeleteRemovesLaneConfig(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{
		ID:        "delete-lane-config",
		CreatedAt: now,
		UpdatedAt: now,
		Cwd:       ws,
	}
	cfg := &session.LaneConfig{
		Model:          "m",
		Provider:       "p",
		ThinkingLevel:  "high",
		ActiveToolNames: []string{"read"},
	}
	if err := st.CreateWithLaneConfig(NewMetadata(header.ID, "Delete", "pigo", "m", ws), header, nil, cfg); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM lane_config WHERE session_id=?`, header.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 1 {
		t.Fatalf("lane_config rows before delete = %d, want 1", before)
	}
	if err := st.Delete(header.ID); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM lane_config WHERE session_id=?`, header.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Fatalf("lane_config rows after delete = %d, want 0", after)
	}
}

func TestCreateWithLaneConfigPersistsConfig(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "create-lane-config", CreatedAt: now, UpdatedAt: now, Cwd: ws}
	cfg := &session.LaneConfig{
		Model:          "openai/m",
		Provider:       "openai",
		ThinkingLevel:  "high",
		ActiveToolNames: []string{"read"},
	}
	if err := st.CreateWithLaneConfig(NewMetadata(header.ID, "Create", "pigo", "m", ws), header, nil, cfg); err != nil {
		t.Fatal(err)
	}
	lanes, err := st.Lanes(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got *session.LaneConfig
	for _, l := range lanes {
		if l.Lane == "main" {
			got = l.Config
		}
	}
	if got == nil || !reflect.DeepEqual(got, cfg) {
		t.Fatalf("lane config = %+v, want %+v", got, cfg)
	}
	proj, err := st.Projection(header.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if proj.Model != cfg.Model || proj.Provider != cfg.Provider || proj.ThinkingLevel != cfg.ThinkingLevel {
		t.Fatalf("projection state = %s/%s/%s, want %s/%s/%s",
			proj.Model, proj.Provider, proj.ThinkingLevel,
			cfg.Model, cfg.Provider, cfg.ThinkingLevel)
	}
}

func TestSetLabelAndProjection(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "label", CreatedAt: now, UpdatedAt: now, Cwd: ws}
	cfg := &session.LaneConfig{Model: "m", Provider: "p", ThinkingLevel: "medium"}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hello")}},
	}
	if err := st.CreateWithLaneConfig(NewMetadata("label", "L", "pigo", "m", ws), header, msgs, cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := st.Entries("label")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLabel("label", entries[0].ID, "start"); err != nil {
		t.Fatal(err)
	}
	proj, err := st.Projection("label", "")
	if err != nil {
		t.Fatal(err)
	}
	if proj.Labels[entries[0].ID] != "start" {
		t.Fatalf("labels = %+v, want start", proj.Labels)
	}
}

func TestListDerivesTitleFromFirstUserMessage(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "title-sess", CreatedAt: now, UpdatedAt: now, Cwd: ws}
	meta := NewMetadata(header.ID, "Session", "pigo", "m", ws)
	if err := st.Create(meta, header, agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("Fix lint warnings\nsecond line")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("done")}},
	}); err != nil {
		t.Fatal(err)
	}
	metas, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].SessionName != "Fix lint warnings" {
		t.Fatalf("list titles = %+v, want derived first user message", metas)
	}
}

func TestFTSSearchScopedByCwd(t *testing.T) {
	home := t.TempDir()
	wsA := filepath.Join(home, "a")
	wsB := filepath.Join(home, "b")
	for _, d := range []string{wsA, wsB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stA, err := OpenForWorkspace(home, wsA)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stA.Close() })
	now := time.Now().UTC()
	hA := session.SessionHeader{ID: "a", CreatedAt: now, UpdatedAt: now, Cwd: wsA}
	hB := session.SessionHeader{ID: "b", CreatedAt: now, UpdatedAt: now, Cwd: wsB}
	if err := stA.Create(NewMetadata("a", "A", "pigo", "m", wsA), hA, agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("needle-a")}},
	}); err != nil {
		t.Fatal(err)
	}
	stB, err := OpenForWorkspace(home, wsB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stB.Close() })
	if err := stB.Create(NewMetadata("b", "B", "pigo", "m", wsB), hB, agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("needle-b")}},
	}); err != nil {
		t.Fatal(err)
	}
	hits, err := stA.Search("needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "a" {
		t.Fatalf("search hits = %+v, want only session a", hits)
	}
}

func TestWriterLeaseTakeoverAfterExpiry(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "lease", CreatedAt: now, UpdatedAt: now, Cwd: ws}
	if err := st.Create(NewMetadata("lease", "L", "pigo", "m", ws), header, nil); err != nil {
		t.Fatal(err)
	}
	// Simulate a stale lease from another owner.
	if _, err := st.db.Exec(`INSERT INTO writer_leases(session_id, owner_id, fence, expires_at_ms) VALUES('lease','dead',7,?)`, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendBranch("lease", header, "", agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("x")}},
	}); err != nil {
		t.Fatal(err)
	}
	var fence int64
	err := st.db.QueryRow(`SELECT fence FROM writer_leases WHERE session_id='lease'`).Scan(&fence)
	if err == nil {
		t.Fatalf("writer lease still present with fence %d", fence)
	}
	if !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
}

func TestV4ExportImportRoundTrip(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "src", CreatedAt: now, UpdatedAt: now, Cwd: ws, Model: "m"}
	cfg := &session.LaneConfig{Model: "m", Provider: "p", ThinkingLevel: "medium"}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("a")}},
	}
	if err := st.CreateWithLaneConfig(NewMetadata("src", "Source", "pigo", "m", ws), header, msgs, cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := st.Entries("src")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLabel("src", entries[0].ID, "question"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "session.jsonl")
	if _, err := st.Export("src", out); err != nil {
		t.Fatal(err)
	}
	newHeader, laneCfg, v4, facts, err := st.ImportV4(out, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if newHeader.ID == "src" || len(v4) != 2 {
		t.Fatalf("import header=%q entries=%d", newHeader.ID, len(v4))
	}
	meta := NewMetadata(newHeader.ID, "Imported", "pigo", newHeader.Model, ws)
	meta.LastActiveAt = newHeader.UpdatedAt
	if err := st.ImportV4EntriesWithLaneConfig(meta, newHeader, v4, facts, laneCfg); err != nil {
		t.Fatal(err)
	}
	proj, err := st.Projection(newHeader.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.Messages) != 2 {
		t.Fatalf("imported messages = %d, want 2", len(proj.Messages))
	}
	importedEntries, _ := st.Entries(newHeader.ID)
	if proj.Labels[importedEntries[0].ID] != "question" {
		t.Fatalf("labels = %+v, want question", proj.Labels)
	}
}

func TestForkV4RejectsMissingLaneConfig(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "src-no-config", CreatedAt: now, UpdatedAt: now, Cwd: ws}
	if err := st.Create(NewMetadata(header.ID, "S", "pigo", "m", ws), header, agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q")}},
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := st.Entries(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.ForkV4(header.ID, entries[len(entries)-1].ID, now); err == nil || !strings.Contains(err.Error(), "no lane config") {
		t.Fatalf("ForkV4 error = %v, want missing lane config", err)
	}
}

func TestImportV4RejectsMissingLaneConfig(t *testing.T) {
	st, ws := openTestStore(t)
	now := time.Now().UTC()
	entry, err := session.NewV4Entry("1", "", now, agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   agentcore.ContentList{agentcore.NewTextContent("q")},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "no-config.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.WriteV4JSONL(f, session.V4Header{
		Type: "session", Version: session.V4SchemaVersion, ID: "old",
		CreatedAt: now, UpdatedAt: now, Cwd: ws,
	}, []session.V4Entry{entry}, nil); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := st.ImportV4(path, now); err == nil || !strings.Contains(err.Error(), "no main lane config") {
		t.Fatalf("ImportV4 error = %v, want missing lane config", err)
	}
}
