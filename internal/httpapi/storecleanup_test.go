package httpapi

import (
	"testing"

	"github.com/smallnest/pigo/internal/sessionstore"
)

func cleanupStores(t *testing.T) {
	t.Helper()
	t.Cleanup(sessionstore.CloseAll)
}
