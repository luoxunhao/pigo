package httpapi

import "testing"

func TestModeServiceDefault(t *testing.T) {
	svc := NewModeService(nil)
	modes := svc.List()
	if len(modes) != 1 || modes[0].Id != "build" {
		t.Fatalf("modes = %+v", modes)
	}
	if !svc.Known("build") {
		t.Fatal("build should be known")
	}
	if svc.Known("plan") {
		t.Fatal("plan should not be known without plugins")
	}
}
