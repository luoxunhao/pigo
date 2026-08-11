package httpapi

import (
	"context"

	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/plugin"
)

// ModeService aggregates built-in and plugin-provided modes.
type ModeService struct {
	manager *plugin.Manager
}

// NewModeService builds a mode service. A nil manager yields only the default mode.
func NewModeService(manager *plugin.Manager) *ModeService {
	return &ModeService{manager: manager}
}

func (m *ModeService) List() []gen.Mode {
	modes := defaultModes()
	if m.manager == nil {
		return modes
	}
	for _, spec := range m.manager.Modes() {
		tools := spec.Tools
		model := spec.Model
		systemPrompt := spec.SystemPrompt
		modes = append(modes, gen.Mode{
			Id:           spec.Name,
			Name:         spec.Name,
			Description:  spec.Description,
			Tools:        &tools,
			Model:        &model,
			SystemPrompt: &systemPrompt,
		})
	}
	return modes
}

func (m *ModeService) Known(mode string) bool {
	if mode == "build" {
		return true
	}
	if m.manager == nil {
		return false
	}
	for _, spec := range m.manager.Modes() {
		if spec.Name == mode {
			return true
		}
	}
	return false
}

func (m *ModeService) Apply(mode, args string) *APIError {
	if m.manager == nil {
		return nil
	}
	for _, p := range m.manager.Plugins() {
		for _, spec := range p.Manifest.Modes {
			if spec.Name == mode {
				_, err := p.ApplyMode(context.Background(), mode, args)
				if err != nil {
					return Internal(err.Error())
				}
				return nil
			}
		}
	}
	return nil
}
