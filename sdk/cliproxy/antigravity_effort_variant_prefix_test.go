package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// With force-model-prefix a credential registers only prefixed names, and the
// conductor hands the executor the name with the prefix stripped; the own
// name still reaches Antigravity under the catalogue id that serves it, an
// aliased one included.
func TestAntigravityForcedPrefixOwnNameResolvesToTheCatalogueID(t *testing.T) {
	cases := []struct {
		name    string
		aliases []config.OAuthModelAlias
		model   string
	}{
		{"catalogue name", nil, "gemini-4-hydrogen"},
		{"aliased name", []config.OAuthModelAlias{{Name: "gemini-4-hydrogen-xhigh", Alias: "hydrogen-xhigh"}}, "hydrogen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{}
			auth := &coreauth.Auth{ID: "ag-forced-prefix-" + tc.model, Provider: "antigravity", Prefix: "tenant"}
			hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{"gemini-4-hydrogen-xhigh": {}}}
			cfg := &config.Config{}
			cfg.ForceModelPrefix = true
			if tc.aliases != nil {
				cfg.OAuthModelAlias = map[string][]config.OAuthModelAlias{"antigravity": tc.aliases}
			}

			models := s.antigravityModelsForHintsWithConfig(auth, hints, cfg)
			for _, model := range models {
				if model.ID == tc.model {
					t.Fatalf("an unprefixed name was registered under force-model-prefix: %v", model)
				}
			}
			modelRegistry := registry.GetGlobalRegistry()
			modelRegistry.RegisterClient(auth.ID, "antigravity", models)
			t.Cleanup(func() { modelRegistry.UnregisterClient(auth.ID) })

			if got := helps.ResolveAntigravityEffortVariant(auth, tc.model, []byte(`{}`), "openai"); got != "gemini-4-hydrogen-xhigh" {
				t.Fatalf("ResolveAntigravityEffortVariant(%q) = %q, want gemini-4-hydrogen-xhigh", tc.model, got)
			}
		})
	}
}
