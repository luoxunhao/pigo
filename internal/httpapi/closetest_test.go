package httpapi

import (
	"os"
	"testing"

	"github.com/smallnest/pigo/internal/sessionstore"
)

func TestMain(m *testing.M) {
	code := m.Run()
	sessionstore.CloseAll()
	os.Exit(code)
}
