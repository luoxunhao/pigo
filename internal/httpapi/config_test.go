package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func TestConfigServiceGetAndUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(path, config.FileConfig{
		Model: "test/a",
		Models: []config.ModelConfig{
			{Provider: "test", ModelID: "a", Name: "A", BaseURL: "http://a", Protocol: "openai", APIKey: "secret"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewConfigService(path)
	got, apiErr := svc.Get()
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if got.Model != "test/a" || len(got.Models) != 1 || got.Models[0].ApiKeyConfigured == nil || !*got.Models[0].ApiKeyConfigured {
		t.Fatalf("got = %+v", got)
	}
	model := "test/a"
	updated, apiErr := svc.Update(gen.UpdateConfigRequest{Model: &model})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if updated.Model != "test/a" {
		t.Fatalf("updated = %+v", updated)
	}
	bad := "nope/nope"
	if _, apiErr := svc.Update(gen.UpdateConfigRequest{Model: &bad}); apiErr == nil {
		t.Fatal("expected unknown model error")
	}
}

func TestConfigServiceProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(path, config.FileConfig{
		Model: "test/a",
		Models: []config.ModelConfig{
			{Provider: "test", ModelID: "a", Name: "A", BaseURL: "http://a", Protocol: "openai"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewConfigService(path)
	providers, apiErr := svc.Providers()
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(providers.Providers) != 1 || providers.Providers[0].Id != "test" {
		t.Fatalf("providers = %+v", providers)
	}
	if apiErr := svc.DeleteProvider("test"); apiErr != nil {
		t.Fatal(apiErr)
	}
	got, apiErr := svc.Get()
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(got.Models) != 0 {
		t.Fatalf("models after delete = %+v", got.Models)
	}
}

func TestConfigServiceDiscover(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1","name":"M1","context_length":128000}]}`))
	}))
	defer ts.Close()
	svc := NewConfigService(filepath.Join(t.TempDir(), "config.toml"))
	protocol := "openai"
	res, apiErr := svc.Discover(gen.DiscoverModelsRequest{BaseUrl: ts.URL, Protocol: &protocol})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(res.Models) != 1 || res.Models[0].ModelId != "m1" {
		t.Fatalf("res = %+v", res)
	}
}
