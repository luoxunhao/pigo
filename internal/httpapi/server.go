package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/plugin"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// CompactFunc runs one manual session compaction and returns a human-readable
// summary.
type CompactFunc func(ctx context.Context, sessionID, directory string) (string, error)

// DreamFunc runs one memory consolidation pass and returns a human-readable
// report.
type DreamFunc func(ctx context.Context, args string) (string, error)

// GoalFunc runs one /goal invocation over serve and streams progress to out.
// beforeToolCall is the serve permission seam installed by the prompt manager.
type GoalFunc func(ctx context.Context, sessionID, directory, args string, out io.Writer, beforeToolCall agentcore.BeforeToolCallFunc) (string, error)

// Config controls the HTTP server behavior.
type Config struct {
	Version             string
	Password            string
	AllowedOrigins      []string
	PigoHome            string
	ConfigPath          string
	PromptRunner        PromptRunner
	TrustPath           string
	PluginManager       *plugin.Manager
	AutoRejectUntrusted bool
	ApproveDirectories  []string
	SlashRegistry       *runtime.SlashRegistry
	CompactFunc         CompactFunc
	DreamFunc           DreamFunc
	GoalFunc            GoalFunc
}

// Server implements the generated HTTP API surface.
type Server struct {
	version  string
	spec     []byte
	doc      []byte
	sessions *SessionService
	events   *EventBroker
	prompts  *PromptManager
	commands *CommandService
	trust    *TrustService
	perms    *PermissionManager
	config   *ConfigService
	modes    *ModeService
	remote   *RemoteControlService
}

// NewServer builds a Server from config.
func NewServer(cfg Config) (*Server, error) {
	spec, err := gen.GetSpecJSON()
	if err != nil {
		return nil, err
	}
	pigoHome := cfg.PigoHome
	if pigoHome == "" {
		pigoHome, err = sessionstore.PigoHome()
		if err != nil {
			return nil, err
		}
	}
	doc := []byte(`<!doctype html><html lang="zh"><head><meta charset="utf-8"><title>pigo HTTP API</title></head><body><h1>pigo HTTP API</h1><p><a href="/api/v1/openapi.json">OpenAPI spec</a></p></body></html>`)
	sessionService := NewSessionService(pigoHome)
	if cfg.ConfigPath != "" {
		sessionService = NewSessionServiceWithConfig(pigoHome, cfg.ConfigPath)
	}
	modeService := NewModeService(cfg.PluginManager)
	sessionService.SetModeKnownChecker(modeService.Known)
	broker := NewEventBroker()
	trustPath := cfg.TrustPath
	if trustPath == "" {
		trustPath = trust.DefaultPath()
	}
	trustManager, err := trust.NewManager(trustPath)
	if err != nil {
		return nil, err
	}
	for _, dir := range cfg.ApproveDirectories {
		if dir != "" {
			trustManager.SetSessionTrust(dir)
		}
	}
	runner := cfg.PromptRunner
	if runner == nil {
		runner = unavailableRunner
	}
	perms := NewPermissionManager(broker)
	prompts := NewPromptManager(runner, broker)
	prompts.SetPermissionSeam(perms, trustManager)
	prompts.SetAutoRejectUntrusted(cfg.AutoRejectUntrusted)
	configPath := cfg.ConfigPath
	if configPath == "" {
		configPath = config.FileConfigPath()
	}
	remote := NewRemoteControlService()
	return &Server{version: cfg.Version, spec: spec, doc: doc, sessions: sessionService, events: broker, prompts: prompts, commands: NewCommandService(sessionService, prompts, cfg.SlashRegistry, cfg.CompactFunc, cfg.DreamFunc, cfg.GoalFunc, remote, broker), trust: NewTrustService(trustManager), perms: perms, config: NewConfigService(configPath), modes: modeService, remote: remote}, nil
}

// NewRouter assembles the chi router with middleware and API routes.
func NewRouter(cfg Config) (http.Handler, error) {
	srv, err := NewServer(cfg)
	if err != nil {
		return nil, err
	}
	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(Recoverer)
	r.Use(CORS(cfg.AllowedOrigins))
	r.Use(BasicAuth(cfg.Password))
	handler := gen.HandlerFromMux(srv, r)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, NotFound(CodeNotFound, "route not found"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, InvalidParams("method not allowed"))
	})
	return handler, nil
}

// GetHealth implements GET /api/v1/health.
func (s *Server) GetHealth(w http.ResponseWriter, _ *http.Request) {
	responseJSON(w, http.StatusOK, gen.Health{Healthy: true, Version: s.version})
}

// GetOpenAPISpec implements GET /api/v1/openapi.json.
func (s *Server) GetOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.spec)
}

// GetAPIDoc implements GET /api/v1/doc.
func (s *Server) GetAPIDoc(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.doc)
}

// ListSessions implements GET /api/v1/session.
func (s *Server) ListSessions(w http.ResponseWriter, r *http.Request, params gen.ListSessionsParams) {
	directory := ""
	if params.Directory != nil {
		directory = *params.Directory
	}
	before := ""
	if params.Before != nil {
		before = *params.Before
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	resp, apiErr := s.sessions.List(directory, before, limit)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// CreateSession implements POST /api/v1/session.
func (s *Server) CreateSession(w http.ResponseWriter, r *http.Request) {
	var body gen.NewSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.sessions.Create(body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// LoadSession implements POST /api/v1/session/{id}/load.
func (s *Server) LoadSession(w http.ResponseWriter, r *http.Request, sessionId string) {
	var body gen.LoadSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.sessions.Load(sessionId, body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// DeleteSession implements DELETE /api/v1/session/{id}.
func (s *Server) DeleteSession(w http.ResponseWriter, r *http.Request, sessionId string, params gen.DeleteSessionParams) {
	if apiErr := s.sessions.Delete(sessionId, params.Directory); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CloseSession implements POST /api/v1/session/{id}/close.
func (s *Server) CloseSession(w http.ResponseWriter, r *http.Request, sessionId string, params gen.CloseSessionParams) {
	if apiErr := s.sessions.Close(sessionId, params.Directory); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetSessionStatus implements GET /api/v1/session/{id}/status.
func (s *Server) GetSessionStatus(w http.ResponseWriter, r *http.Request, sessionId string, params gen.GetSessionStatusParams) {
	resp, apiErr := s.sessions.Status(sessionId, params.Directory)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// UpdateSessionConfig implements PATCH /api/v1/session/{id}.
func (s *Server) UpdateSessionConfig(w http.ResponseWriter, r *http.Request, sessionId string) {
	var body gen.UpdateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.sessions.UpdateConfig(sessionId, body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// SetSessionMode implements POST /api/v1/session/{id}/mode.
func (s *Server) SetSessionMode(w http.ResponseWriter, r *http.Request, sessionId string) {
	var body gen.SetModeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	if !s.modes.Known(body.ModeId) {
		WriteError(w, r, &APIError{Status: http.StatusBadRequest, Code: CodeModeNotFound, Message: "unknown mode: " + body.ModeId})
		return
	}
	if apiErr := s.modes.Apply(body.ModeId, ""); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	resp, apiErr := s.sessions.SetMode(sessionId, body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// GetSessionMessages implements GET /api/v1/session/{id}/messages.
func (s *Server) GetSessionMessages(w http.ResponseWriter, r *http.Request, sessionId string, params gen.GetSessionMessagesParams) {
	directory := params.Directory
	before := ""
	if params.Before != nil {
		before = *params.Before
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	resp, apiErr := s.sessions.Messages(sessionId, directory, before, limit)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// PromptSession implements POST /api/v1/session/{id}/prompt.
func (s *Server) PromptSession(w http.ResponseWriter, r *http.Request, sessionId string) {
	var body gen.PromptRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.prompts.SubmitSync(sessionId, body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// PromptSessionAsync implements POST /api/v1/session/{id}/prompt_async.
func (s *Server) PromptSessionAsync(w http.ResponseWriter, r *http.Request, sessionId string) {
	var body gen.PromptRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.prompts.SubmitAsync(sessionId, body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusAccepted, resp)
}

// CancelSessionPrompt implements POST /api/v1/session/{id}/cancel.
func (s *Server) CancelSessionPrompt(w http.ResponseWriter, r *http.Request, sessionId string) {
	if apiErr := s.prompts.Cancel(sessionId); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCommands implements GET /api/v1/commands.
func (s *Server) ListCommands(w http.ResponseWriter, _ *http.Request, _ gen.ListCommandsParams) {
	responseJSON(w, http.StatusOK, s.commands.List())
}

// ExecuteCommand implements POST /api/v1/session/{id}/command.
func (s *Server) ExecuteCommand(w http.ResponseWriter, r *http.Request, sessionId string) {
	var body gen.CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.commands.Execute(r.Context(), sessionId, body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// ListTrust implements GET /api/v1/permission/trust.
func (s *Server) ListTrust(w http.ResponseWriter, _ *http.Request) {
	responseJSON(w, http.StatusOK, s.trust.List())
}

// SetTrust implements POST /api/v1/permission/trust.
func (s *Server) SetTrust(w http.ResponseWriter, r *http.Request) {
	var body gen.SetTrustRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	if apiErr := s.trust.Set(body); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteTrust implements DELETE /api/v1/permission/trust.
func (s *Server) DeleteTrust(w http.ResponseWriter, r *http.Request, params gen.DeleteTrustParams) {
	if apiErr := s.trust.Delete(params.Path); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReplyPermission implements POST /api/v1/session/{id}/permissions/{permissionId}/reply.
func (s *Server) ReplyPermission(w http.ResponseWriter, r *http.Request, sessionId, permissionId string) {
	var body gen.PermissionReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	if apiErr := s.perms.Reply(sessionId, permissionId, body.OptionId); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetConfig implements GET /api/v1/config.
func (s *Server) GetConfig(w http.ResponseWriter, r *http.Request) {
	resp, apiErr := s.config.Get()
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// UpdateConfig implements PATCH /api/v1/config.
func (s *Server) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	var body gen.UpdateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.config.Update(body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// ListProviders implements GET /api/v1/config/providers.
func (s *Server) ListProviders(w http.ResponseWriter, r *http.Request) {
	resp, apiErr := s.config.Providers()
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// UpsertProvider implements PUT /api/v1/config/providers/{providerId}.
func (s *Server) UpsertProvider(w http.ResponseWriter, r *http.Request, providerId string) {
	var body gen.ProviderInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	if apiErr := s.config.UpsertProvider(providerId, body); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteProvider implements DELETE /api/v1/config/providers/{providerId}.
func (s *Server) DeleteProvider(w http.ResponseWriter, r *http.Request, providerId string) {
	if apiErr := s.config.DeleteProvider(providerId); apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListModes implements GET /api/v1/modes.
func (s *Server) ListModes(w http.ResponseWriter, _ *http.Request, _ gen.ListModesParams) {
	responseJSON(w, http.StatusOK, gen.ModesResult{Modes: s.modes.List()})
}

// DiscoverModels implements POST /api/v1/config/providers/discover.
func (s *Server) DiscoverModels(w http.ResponseWriter, r *http.Request) {
	var body gen.DiscoverModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.config.Discover(body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}

// TestModel implements POST /api/v1/config/providers/test.
func (s *Server) TestModel(w http.ResponseWriter, r *http.Request) {
	var body gen.TestModelRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, r, InvalidParams("invalid request body"))
		return
	}
	resp, apiErr := s.config.TestModel(body)
	if apiErr != nil {
		WriteError(w, r, apiErr)
		return
	}
	responseJSON(w, http.StatusOK, resp)
}
