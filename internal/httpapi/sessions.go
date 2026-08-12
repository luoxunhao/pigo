package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// SessionService owns session persistence for the HTTP API.
type SessionService struct {
	pigoHome   string
	configPath string
	mu         sync.Mutex
	stores     map[string]*sessionstore.Store
	modeKnown  func(string) bool
}

// NewSessionService builds a session service rooted at pigoHome.
func NewSessionService(pigoHome string) *SessionService {
	return NewSessionServiceWithConfig(pigoHome, config.FileConfigPath())
}

// NewSessionServiceWithConfig builds a session service with an explicit config path.
func NewSessionServiceWithConfig(pigoHome, configPath string) *SessionService {
	return &SessionService{pigoHome: pigoHome, configPath: configPath, stores: make(map[string]*sessionstore.Store), modeKnown: knownMode}
}

// SetModeKnownChecker overrides the mode validation function used by config updates.
func (s *SessionService) SetModeKnownChecker(fn func(string) bool) {
	s.modeKnown = fn
}

func (s *SessionService) storeFor(directory string) (*sessionstore.Store, error) {
	slug := sessionstore.WorkspaceSlug(directory)
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.stores[slug]; ok {
		return store, nil
	}
	store, err := sessionstore.OpenForWorkspace(s.pigoHome, directory)
	if err != nil {
		return nil, err
	}
	s.stores[slug] = store
	return store, nil
}

// Create creates a new persisted session.
func (s *SessionService) Create(req gen.NewSessionRequest) (gen.Session, *APIError) {
	if req.Directory == "" || !filepath.IsAbs(req.Directory) {
		return gen.Session{}, InvalidParams("directory must be an absolute path")
	}
	cfg, err := config.LoadFileConfig(s.configPath)
	if err != nil {
		return gen.Session{}, Internal(err.Error())
	}
	model := ""
	if req.Model != nil {
		model = *req.Model
	}
	if model == "" {
		model = cfg.Model
	}
	if model == "" {
		return gen.Session{}, &APIError{Status: http.StatusConflict, Code: CodeModelNotConfigured, Message: "no default model configured"}
	}
	if _, ok := cfg.FindModel(model); !ok {
		return gen.Session{}, &APIError{Status: http.StatusBadRequest, Code: CodeModelNotFound, Message: "unknown modelId: " + model}
	}
	store, err := s.storeFor(req.Directory)
	if err != nil {
		return gen.Session{}, Internal(err.Error())
	}
	now := time.Now().UTC()
	id := session.NewID(now)
	title := "Session"
	if req.Title != nil && *req.Title != "" {
		title = *req.Title
	}
	header := session.SessionHeader{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
		Model:     model,
		Cwd:       req.Directory,
	}
	meta := sessionstore.NewMetadata(id, title, "pigo", model, req.Directory)
	if err := store.Create(meta, header, nil); err != nil {
		return gen.Session{}, Internal(err.Error())
	}
	mode := "build"
	if req.Mode != nil && *req.Mode != "" {
		mode = *req.Mode
	}
	additional := []string{}
	if req.AdditionalDirectories != nil {
		additional = *req.AdditionalDirectories
	}
	_ = additional
	return s.sessionResponse(id, req.Directory, model, mode, cfg), nil
}

// List returns sessions filtered by directory and cursor.
func (s *SessionService) List(directory, before string, limit int, includeSubagents bool) (gen.SessionListResult, *APIError) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var metas []sessionstore.Metadata
	var err error
	if directory == "" {
		metas, err = sessionstore.ListAll(s.pigoHome)
	} else {
		if !filepath.IsAbs(directory) {
			return gen.SessionListResult{}, InvalidParams("directory must be an absolute path")
		}
		var store *sessionstore.Store
		store, err = s.storeFor(directory)
		if err == nil {
			metas, err = store.List()
		}
	}
	if err != nil {
		return gen.SessionListResult{}, Internal(err.Error())
	}
	if !includeSubagents {
		metas = nonSubagentSessions(metas)
	}
	offset := 0
	if before != "" {
		if n, convErr := strconv.Atoi(before); convErr == nil && n > 0 {
			offset = n
		}
	}
	end := offset + limit
	if end > len(metas) {
		end = len(metas)
	}
	if offset > len(metas) {
		offset = len(metas)
	}
	sessions := make([]gen.SessionSummary, 0, end-offset)
	for _, m := range metas[offset:end] {
		title := m.SessionName
		updatedAt := m.LastActiveAt.Format(time.RFC3339)
		sessions = append(sessions, gen.SessionSummary{
			SessionId: m.SessionID,
			Directory: m.WorkspacePath,
			Title:     &title,
			UpdatedAt: &updatedAt,
			ParentSessionId:  optString(m.ParentSessionID),
			ParentToolCallId: optString(m.ParentToolCallID),
			SubagentType:     optString(m.SubagentType),
			SessionKind:      optString(m.SessionKind),
		})
	}
	var next *string
	if end < len(metas) {
		v := strconv.Itoa(end)
		next = &v
	}
	return gen.SessionListResult{Sessions: sessions, NextCursor: next}, nil
}

// Load restores session metadata and a message window.
func (s *SessionService) Load(sessionID string, req gen.LoadSessionRequest) (gen.SessionLoadResult, *APIError) {
	if req.Directory == "" || !filepath.IsAbs(req.Directory) {
		return gen.SessionLoadResult{}, InvalidParams("directory must be an absolute path")
	}
	store, err := s.storeFor(req.Directory)
	if err != nil {
		return gen.SessionLoadResult{}, Internal(err.Error())
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return gen.SessionLoadResult{}, NotFound(CodeSessionNotFound, "session not found: "+sessionID)
	}
	if req.LeafId != nil && *req.LeafId != "" {
		if apiErr := s.moveLeaf(store, sessionID, *req.LeafId); apiErr != nil {
			return gen.SessionLoadResult{}, apiErr
		}
	}
	proj, err := store.Projection(sessionID, "")
	if err != nil {
		return gen.SessionLoadResult{}, Internal(err.Error())
	}
	limit := 50
	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
	}
	if limit > 200 {
		limit = 200
	}
	offset := 0
	if req.Before != nil && *req.Before != "" {
		if n, convErr := strconv.Atoi(*req.Before); convErr == nil && n > 0 {
			offset = n
		}
	}
	end := len(proj.Entries) - offset
	if end < 0 {
		end = 0
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	messages := make([]gen.Message, 0, end-start)
	for i, e := range proj.Entries[start:end] {
		messages = append(messages, v4EntryToDomainMessage(e, start+i, proj.Lane))
	}
	var next *string
	if start > 0 && len(messages) > 0 {
		v := strconv.Itoa(start)
		next = &v
	}
	cfg, cfgErr := config.LoadFileConfig(s.configPath)
	if cfgErr != nil {
		return gen.SessionLoadResult{}, Internal(cfgErr.Error())
	}
	return gen.SessionLoadResult{
		SessionId:     sessionID,
		Directory:     req.Directory,
		ConfigOptions: sessionConfigOptions(cfg, meta.ModelName, "build"),
		Messages:      messages,
		HasMore:       start > 0,
		NextCursor:    next,
		CurrentLeafId: optString(proj.LeafID),
		CurrentLane:   optString(proj.Lane),
		Lanes:         lanesToGen(proj.Lanes),
	}, nil
}

// Messages returns a paginated message window.
func (s *SessionService) Messages(sessionID, directory, before string, limit int) (gen.MessageListResult, *APIError) {
	if directory == "" || !filepath.IsAbs(directory) {
		return gen.MessageListResult{}, InvalidParams("directory must be an absolute path")
	}
	store, err := s.storeFor(directory)
	if err != nil {
		return gen.MessageListResult{}, Internal(err.Error())
	}
	if _, err := store.LoadMetadata(sessionID); err != nil {
		return gen.MessageListResult{}, NotFound(CodeSessionNotFound, "session not found: "+sessionID)
	}
	proj, err := store.Projection(sessionID, "")
	if err != nil {
		return gen.MessageListResult{}, Internal(err.Error())
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := 0
	if before != "" {
		if n, convErr := strconv.Atoi(before); convErr == nil && n > 0 {
			offset = n
		}
	}
	end := len(proj.Entries) - offset
	if end < 0 {
		end = 0
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	messages := make([]gen.Message, 0, end-start)
	for i, e := range proj.Entries[start:end] {
		messages = append(messages, v4EntryToDomainMessage(e, start+i, proj.Lane))
	}
	var next *string
	if start > 0 && len(messages) > 0 {
		v := strconv.Itoa(start)
		next = &v
	}
	return gen.MessageListResult{Messages: messages, HasMore: start > 0, NextCursor: next}, nil
}

// Delete removes a session idempotently.
func (s *SessionService) Delete(sessionID, directory string) *APIError {
	if directory == "" || !filepath.IsAbs(directory) {
		return InvalidParams("directory must be an absolute path")
	}
	store, err := s.storeFor(directory)
	if err != nil {
		return Internal(err.Error())
	}
	if err := store.Delete(sessionID); err != nil {
		return Internal(err.Error())
	}
	return nil
}

// Close releases live state while preserving history.
func (s *SessionService) Close(sessionID, directory string) *APIError {
	if directory == "" || !filepath.IsAbs(directory) {
		return InvalidParams("directory must be an absolute path")
	}
	store, err := s.storeFor(directory)
	if err != nil {
		return Internal(err.Error())
	}
	if _, err := store.LoadMetadata(sessionID); err != nil {
		return NotFound(CodeSessionNotFound, "session not found: "+sessionID)
	}
	return nil
}

// Status returns the current session status.
func (s *SessionService) Status(sessionID, directory string) (gen.SessionStatusResult, *APIError) {
	if directory == "" || !filepath.IsAbs(directory) {
		return gen.SessionStatusResult{}, InvalidParams("directory must be an absolute path")
	}
	store, err := s.storeFor(directory)
	if err != nil {
		return gen.SessionStatusResult{}, Internal(err.Error())
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return gen.SessionStatusResult{}, NotFound(CodeSessionNotFound, "session not found: "+sessionID)
	}
	proj, err := store.Projection(sessionID, "")
	if err != nil {
		return gen.SessionStatusResult{}, Internal(err.Error())
	}
	model := meta.ModelName
	mode := "build"
	thinking := "medium"
	if proj.Model != "" {
		model = proj.Model
	}
	if proj.ThinkingLevel != "" {
		thinking = proj.ThinkingLevel
	}
	mode = sessionMode(meta)
	queued := 0
	return gen.SessionStatusResult{
		SessionId:     sessionID,
		Status:        "idle",
		Model:         &model,
		Mode:          &mode,
		ThinkingLevel: &thinking,
		QueuedCount:   &queued,
		CurrentLeafId: optString(proj.LeafID),
		CurrentLane:   optString(proj.Lane),
		Lanes:         lanesToGen(proj.Lanes),
	}, nil
}

// UpdateConfig updates session-level model, thinking level, or mode.
func (s *SessionService) UpdateConfig(sessionID string, req gen.UpdateSessionRequest) (gen.ConfigOptionsResult, *APIError) {
	if req.Directory == "" || !filepath.IsAbs(req.Directory) {
		return gen.ConfigOptionsResult{}, InvalidParams("directory must be an absolute path")
	}
	store, err := s.storeFor(req.Directory)
	if err != nil {
		return gen.ConfigOptionsResult{}, Internal(err.Error())
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return gen.ConfigOptionsResult{}, NotFound(CodeSessionNotFound, "session not found: "+sessionID)
	}
	cfg, err := config.LoadFileConfig(s.configPath)
	if err != nil {
		return gen.ConfigOptionsResult{}, Internal(err.Error())
	}
	if req.Model != nil && *req.Model != "" {
		if _, ok := cfg.FindModel(*req.Model); !ok {
			return gen.ConfigOptionsResult{}, &APIError{Status: http.StatusBadRequest, Code: CodeModelNotFound, Message: "unknown modelId: " + *req.Model}
		}
		meta.ModelName = *req.Model
	}
	custom := readSessionCustom(meta)
	if req.ThinkingLevel != nil && *req.ThinkingLevel != "" {
		if !validThinkingLevel(*req.ThinkingLevel) {
			return gen.ConfigOptionsResult{}, InvalidParams("unknown thinking level: " + *req.ThinkingLevel)
		}
		custom["thinkingLevel"] = *req.ThinkingLevel
	}
	if req.Mode != nil && *req.Mode != "" {
		if !s.modeKnown(*req.Mode) {
			return gen.ConfigOptionsResult{}, &APIError{Status: http.StatusBadRequest, Code: CodeModeNotFound, Message: "unknown mode: " + *req.Mode}
		}
		custom["mode"] = *req.Mode
	}
	meta.CustomMetadata = writeSessionCustom(custom)
	if err := store.SaveMetadata(meta); err != nil {
		return gen.ConfigOptionsResult{}, Internal(err.Error())
	}
	mode := sessionMode(meta)
	thinking := sessionThinking(meta)
	model := meta.ModelName
	return gen.ConfigOptionsResult{ConfigOptions: sessionConfigOptions(cfg, model, mode, thinking)}, nil
}

// Rename updates the session display name.
func (s *SessionService) Rename(sessionID, directory, name string) *APIError {
	if directory == "" || !filepath.IsAbs(directory) {
		return InvalidParams("directory must be an absolute path")
	}
	store, err := s.storeFor(directory)
	if err != nil {
		return Internal(err.Error())
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return NotFound(CodeSessionNotFound, "session not found: "+sessionID)
	}
	meta.SessionName = name
	if err := store.SaveMetadata(meta); err != nil {
		return Internal(err.Error())
	}
	return nil
}

// SetMode switches the session mode.
func (s *SessionService) SetMode(sessionID string, req gen.SetModeRequest) (gen.ModeResult, *APIError) {
	if req.Directory == "" || !filepath.IsAbs(req.Directory) {
		return gen.ModeResult{}, InvalidParams("directory must be an absolute path")
	}
	if !s.modeKnown(req.ModeId) {
		return gen.ModeResult{}, &APIError{Status: http.StatusBadRequest, Code: CodeModeNotFound, Message: "unknown mode: " + req.ModeId}
	}
	store, err := s.storeFor(req.Directory)
	if err != nil {
		return gen.ModeResult{}, Internal(err.Error())
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return gen.ModeResult{}, NotFound(CodeSessionNotFound, "session not found: "+sessionID)
	}
	custom := readSessionCustom(meta)
	custom["mode"] = req.ModeId
	meta.CustomMetadata = writeSessionCustom(custom)
	if err := store.SaveMetadata(meta); err != nil {
		return gen.ModeResult{}, Internal(err.Error())
	}
	return gen.ModeResult{CurrentModeId: req.ModeId, AvailableModes: defaultModes()}, nil
}

func readSessionCustom(meta sessionstore.Metadata) map[string]any {
	custom := map[string]any{}
	if len(meta.CustomMetadata) > 0 {
		_ = json.Unmarshal(meta.CustomMetadata, &custom)
	}
	return custom
}

func writeSessionCustom(custom map[string]any) json.RawMessage {
	b, _ := json.Marshal(custom)
	return b
}

func sessionMode(meta sessionstore.Metadata) string {
	custom := readSessionCustom(meta)
	if v, ok := custom["mode"].(string); ok && v != "" {
		return v
	}
	return "build"
}

func sessionThinking(meta sessionstore.Metadata) string {
	custom := readSessionCustom(meta)
	if v, ok := custom["thinkingLevel"].(string); ok && v != "" {
		return v
	}
	return "medium"
}

func knownMode(mode string) bool {
	return mode == "build"
}

func validThinkingLevel(level string) bool {
	switch level {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func defaultModes() []gen.Mode {
	return []gen.Mode{{Id: "build", Name: "Build", Description: "Default mode"}}
}

func (s *SessionService) sessionResponse(id, directory, model, mode string, cfg config.FileConfig) gen.Session {
	return gen.Session{
		SessionId:       id,
		Directory:       directory,
		ConfigOptions:   sessionConfigOptions(cfg, model, mode),
		AvailableModes:  []gen.Mode{{Id: "build", Name: "Build", Description: "Default mode"}},
		AvailableCommands: []gen.AvailableCommand{},
	}
}

func sessionConfigOptions(cfg config.FileConfig, model, mode string, thinking ...string) []gen.ConfigOption {
	currentThinking := "medium"
	if len(thinking) > 0 && thinking[0] != "" {
		currentThinking = thinking[0]
	}
	modelOptions := make([]map[string]interface{}, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		if !m.IsEnabled() {
			continue
		}
		modelOptions = append(modelOptions, map[string]interface{}{
			"value": m.Key(),
			"name":  configuredModelName(m),
		})
	}
	thoughtOptions := []map[string]interface{}{
		{"value": "off", "name": "Off"},
		{"value": "low", "name": "Low"},
		{"value": "medium", "name": "Medium"},
		{"value": "high", "name": "High"},
	}
	modeOptions := []map[string]interface{}{
		{"value": "build", "name": "Build"},
	}
	options := []gen.ConfigOption{
		{Id: "model", Name: "Model", Type: "select", CurrentValue: &model, Options: &modelOptions},
		{Id: "thought_level", Name: "Thinking Level", Type: "select", CurrentValue: &currentThinking, Options: &thoughtOptions},
		{Id: "mode", Name: "Mode", Type: "select", CurrentValue: &mode, Options: &modeOptions},
	}
	return options
}

func configuredModelName(m config.ModelConfig) string {
	if m.Name != "" {
		return m.Name
	}
	return m.Key()
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func lanesToGen(lanes []session.LaneState) *[]gen.LaneState {
	out := make([]gen.LaneState, 0, len(lanes))
	for _, l := range lanes {
		item := gen.LaneState{Lane: l.Lane}
		if l.LeafID != nil {
			item.LeafId = l.LeafID
		}
		out = append(out, item)
	}
	return &out
}

func (s *SessionService) moveLeaf(store *sessionstore.Store, sessionID, leafID string) *APIError {
	target := leafID
	if err := store.MoveLane(sessionID, "main", &target); err != nil {
		return NotFound(CodeSessionNotFound, "leaf not found: "+leafID)
	}
	return nil
}
