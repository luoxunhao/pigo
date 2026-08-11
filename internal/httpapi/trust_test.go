package httpapi

import (
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/trust"
)

func TestTrustServiceSetListDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	mgr, err := trust.NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewTrustService(mgr)
	dir := filepath.Join(t.TempDir(), "project")
	if apiErr := svc.Set(gen.SetTrustRequest{Path: dir, Decision: "trusted"}); apiErr != nil {
		t.Fatal(apiErr)
	}
	list := svc.List()
	if len(list.Entries) != 1 || list.Entries[0].Decision != "trusted" {
		t.Fatalf("list = %+v", list)
	}
	if apiErr := svc.Delete(dir); apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(svc.List().Entries) != 0 {
		t.Fatalf("entries after delete = %+v", svc.List().Entries)
	}
}

func TestTrustServiceRejectsInvalidDecision(t *testing.T) {
	mgr, err := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewTrustService(mgr)
	if apiErr := svc.Set(gen.SetTrustRequest{Path: t.TempDir(), Decision: "maybe"}); apiErr == nil {
		t.Fatal("expected invalid decision error")
	}
}
