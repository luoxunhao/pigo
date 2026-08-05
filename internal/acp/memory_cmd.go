package acp

import (
	"github.com/smallnest/pigo/internal/memory"
)

// MemoryConfig carries the optional persistent-memory store for /memory.
type MemoryConfig struct {
	Store *memory.Store
}
