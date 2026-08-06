package config

import "strings"

// ProviderModelConfig is one cached model discovered from a custom provider.
type ProviderModelConfig struct {
	ModelID string `toml:"model_id" json:"modelId"`
	Name    string `toml:"name" json:"name"`
}

// ProviderConfig is one entry in the custom provider registry. APIKey is a
// secret and must never be echoed by callers that expose the registry.
type ProviderConfig struct {
	ID       string                `toml:"id" json:"providerId"`
	Name     string                `toml:"name" json:"name"`
	BaseURL  string                `toml:"base_url" json:"baseUrl"`
	APIKey   string                `toml:"api_key" json:"-"`
	Protocol string                `toml:"protocol" json:"protocol"`
	Models   []ProviderModelConfig `toml:"models" json:"models"`
}

// CustomProvider returns the registry entry with the given id.
func (f FileConfig) CustomProvider(id string) (ProviderConfig, bool) {
	for _, p := range f.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return ProviderConfig{}, false
}

// UpsertProvider inserts or replaces a custom provider by id. It reports
// whether the entry already existed.
func (f *FileConfig) UpsertProvider(p ProviderConfig) bool {
	for i := range f.Providers {
		if f.Providers[i].ID == p.ID {
			f.Providers[i] = p
			return true
		}
	}
	f.Providers = append(f.Providers, p)
	return false
}

// DeleteProvider removes a custom provider by id. It reports whether the entry
// existed.
func (f *FileConfig) DeleteProvider(id string) bool {
	for i := range f.Providers {
		if f.Providers[i].ID == id {
			f.Providers = append(f.Providers[:i], f.Providers[i+1:]...)
			return true
		}
	}
	return false
}

// CustomProviderID derives a stable custom provider id from a user-facing name
// or base URL. The id is generated once at creation and never contains "/" so
// it can prefix a model id as providerId/modelId.
func CustomProviderID(name, baseURL string) string {
	src := strings.TrimSpace(name)
	if src == "" {
		src = stripScheme(strings.TrimSpace(baseURL))
	}
	slug := slugify(src)
	if slug == "" {
		slug = "provider"
	}
	return "custom-" + slug
}

func stripScheme(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		return s[i+3:]
	}
	return s
}

// SplitCustomModelID splits "custom-<slug>/<modelId>" into its provider and
// model parts. Non-custom ids return ok=false.
func SplitCustomModelID(model string) (providerID, modelID string, ok bool) {
	if !strings.HasPrefix(model, "custom-") {
		return "", "", false
	}
	i := strings.Index(model, "/")
	if i <= 0 || i == len(model)-1 {
		return "", "", false
	}
	return model[:i], model[i+1:], true
}

// slugify lowercases s and rewrites every run of non-alphanumeric characters to
// a single hyphen, trimming leading/trailing hyphens.
func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
