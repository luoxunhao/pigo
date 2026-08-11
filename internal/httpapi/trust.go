package httpapi

import (
	"path/filepath"

	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/trust"
)

// TrustService exposes persisted trust decisions over HTTP.
type TrustService struct {
	manager *trust.Manager
}

// NewTrustService builds a trust service.
func NewTrustService(manager *trust.Manager) *TrustService {
	return &TrustService{manager: manager}
}

func (s *TrustService) List() gen.TrustListResult {
	entries := make([]gen.TrustEntry, 0)
	for _, e := range s.manager.Entries() {
		entries = append(entries, gen.TrustEntry{Path: e.Path, Decision: e.Decision.String()})
	}
	return gen.TrustListResult{Entries: entries}
}

func (s *TrustService) Set(req gen.SetTrustRequest) *APIError {
	if req.Path == "" || !filepath.IsAbs(req.Path) {
		return InvalidParams("path must be an absolute path")
	}
	var decision trust.Decision
	switch req.Decision {
	case "trusted":
		decision = trust.Trusted
	case "untrusted":
		decision = trust.Untrusted
	case "undecided":
		decision = trust.Undecided
	default:
		return InvalidParams("decision must be trusted, untrusted, or undecided")
	}
	if err := s.manager.SetDecision(req.Path, decision); err != nil {
		return Internal(err.Error())
	}
	return nil
}

func (s *TrustService) Delete(path string) *APIError {
	if path == "" || !filepath.IsAbs(path) {
		return InvalidParams("path must be an absolute path")
	}
	if err := s.manager.Forget(path); err != nil {
		return Internal(err.Error())
	}
	return nil
}

func (s *TrustService) AllowAlways(path string) *APIError {
	return s.Set(gen.SetTrustRequest{Path: path, Decision: "trusted"})
}
