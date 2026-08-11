package httpapi

import (
	"sync"
	"time"
)

const permissionTimeout = 60 * time.Second

// PermissionManager tracks pending permission requests.
type PermissionManager struct {
	broker  *EventBroker
	mu      sync.Mutex
	pending map[string]chan string
}

// NewPermissionManager builds a manager.
func NewPermissionManager(broker *EventBroker) *PermissionManager {
	return &PermissionManager{broker: broker, pending: make(map[string]chan string)}
}

// Ask publishes a permission request and waits for a reply.
func (m *PermissionManager) Ask(sessionID string, toolCall map[string]any, options []map[string]any) (string, *APIError) {
	permissionID := newPermissionID()
	ch := make(chan string, 1)
	m.mu.Lock()
	m.pending[permissionID] = ch
	m.mu.Unlock()
	m.broker.Publish("permission.asked", map[string]any{
		"sessionId":    sessionID,
		"permissionId": permissionID,
		"toolCall":     toolCall,
		"options":      options,
	})
	select {
	case option := <-ch:
		return option, nil
	case <-time.After(permissionTimeout):
		m.mu.Lock()
		delete(m.pending, permissionID)
		m.mu.Unlock()
		return "", &APIError{Status: 410, Code: CodePermissionExpired, Message: "permission request expired"}
	}
}

// Reply resolves a pending permission request.
func (m *PermissionManager) Reply(sessionID, permissionID, optionID string) *APIError {
	m.mu.Lock()
	ch, ok := m.pending[permissionID]
	if ok {
		delete(m.pending, permissionID)
	}
	m.mu.Unlock()
	if !ok {
		return &APIError{Status: 410, Code: CodePermissionExpired, Message: "permission request not found"}
	}
	ch <- optionID
	return nil
}

func newPermissionID() string {
	return "perm-" + newMessageID()[4:]
}
