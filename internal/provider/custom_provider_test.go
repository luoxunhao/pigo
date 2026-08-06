package provider

import (
	"strings"
	"testing"
)

func TestResolveConfiguredProviderOpenAI(t *testing.T) {
	p, err := ResolveConfiguredProvider("custom-gw", "https://gw.example/v1", "openai",
		[]Model{{Provider: "", ID: "m1", DisplayName: "M1"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "custom-gw" {
		t.Fatalf("name = %q, want custom-gw", p.Name())
	}
	models := p.Models()
	if len(models) != 1 || models[0].ID != "m1" || models[0].Provider != "custom-gw" {
		t.Fatalf("models = %+v", models)
	}
}

func TestResolveConfiguredProviderProtocols(t *testing.T) {
	for _, protocol := range []string{"responses", "openai/resp_api", "anthropic", "gemini", ""} {
		if _, err := ResolveConfiguredProvider("custom-p", "https://gw.example/v1", protocol, nil); err != nil {
			t.Errorf("protocol %q: %v", protocol, err)
		}
	}
}

func TestResolveConfiguredProviderErrors(t *testing.T) {
	if _, err := ResolveConfiguredProvider("", "https://x", "openai", nil); err == nil {
		t.Fatal("empty id should error")
	}
	if _, err := ResolveConfiguredProvider("custom-x", "", "openai", nil); err == nil {
		t.Fatal("empty base url should error")
	}
	if _, err := ResolveConfiguredProvider("custom-x", "https://x", "unknown", nil); err == nil ||
		!strings.Contains(err.Error(), "unknown protocol") {
		t.Fatalf("unknown protocol error = %v", err)
	}
}
