package httpapi

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/smallnest/pigo/internal/remotecontrol"
)

// RemoteControlService owns one LAN mirror server per session.
type RemoteControlService struct {
	mu     sync.Mutex
	active map[string]*remoteControlSession
}

type remoteControlSession struct {
	server *remotecontrol.Server
	bridge *remotecontrol.Bridge
	url    string
}

// NewRemoteControlService builds an empty service.
func NewRemoteControlService() *RemoteControlService {
	return &RemoteControlService{active: make(map[string]*remoteControlSession)}
}

// Run handles /remote-control [stop|status].
func (s *RemoteControlService) Run(sessionID, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	switch arg {
	case "stop":
		return s.stop(sessionID)
	case "status":
		return s.status(sessionID)
	default:
		return s.start(sessionID)
	}
}

func (s *RemoteControlService) start(sessionID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs, ok := s.active[sessionID]; ok {
		return "remote control already running: " + rs.url, nil
	}
	srv := remotecontrol.NewServer(remotecontrol.Config{}, nil)
	bridge := remotecontrol.NewBridge(srv)
	srv.SetHandler(bridge)
	url, err := srv.Start()
	if err != nil {
		return "", err
	}
	s.active[sessionID] = &remoteControlSession{server: srv, bridge: bridge, url: url}
	return "Remote control started. Open this URL on a device on the same network:\n\n  " + url + "\n\nRun /remote-control stop to end the session.", nil
}

func (s *RemoteControlService) stop(sessionID string) (string, error) {
	s.mu.Lock()
	rs, ok := s.active[sessionID]
	if ok {
		delete(s.active, sessionID)
	}
	s.mu.Unlock()
	if !ok {
		return "remote control is not running", nil
	}
	_ = rs.server.Stop(context.Background())
	return "remote control stopped", nil
}

func (s *RemoteControlService) status(sessionID string) (string, error) {
	s.mu.Lock()
	rs, ok := s.active[sessionID]
	s.mu.Unlock()
	if !ok {
		return "remote control: off", nil
	}
	state := "waiting for a browser to connect"
	if rs.bridge.Enabled() {
		state = "browser connected"
	}
	return fmt.Sprintf("remote control: on (%s)\n  %s", state, rs.url), nil
}
