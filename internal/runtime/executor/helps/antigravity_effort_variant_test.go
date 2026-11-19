package helps

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func registerAntigravityEffortVariants(t *testing.T, authID string, models []*registry.ModelInfo) *cliproxyauth.Auth {
	t.Helper()
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(authID, "antigravity", registry.FoldEffortVariants(models))
	t.Cleanup(func() { modelRegistry.UnregisterClient(authID) })
	return &cliproxyauth.Auth{ID: authID, Provider: "antigravity"}
}

func TestResolveAntigravityEffortVariantMapsTheModelsOwnNameToItsCatalogueID(t *testing.T) {
	auth := registerAntigravityEffortVariants(t, "helps-effort-variant", []*registry.ModelInfo{
		{ID: "claude-opus-5-5-high", Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}}},
		{ID: "gemini-4-hydrogen-xhigh", Thinking: &registry.ThinkingSupport{Levels: []string{"xhigh"}}},
		{ID: "gemini-4-argon-high", Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}}},
		{ID: "gemini-3-flash", Thinking: &registry.ThinkingSupport{Levels: []string{"minimal", "high"}}},
	})
	cases := []struct {
		name, model, payload, want string
	}{
		{"own name", "claude-opus-5-5", `{}`, "claude-opus-5-5-high"},
		{"own name with suffix", "gemini-4-hydrogen(low)", `{}`, "gemini-4-hydrogen-xhigh(low)"},
		{"own name with body effort", "gemini-4-argon", `{"reasoning_effort":"medium"}`, "gemini-4-argon-high"},
		{"effort-named id", "claude-opus-5-5-high(low)", `{}`, "claude-opus-5-5-high(low)"},
		{"catalogue id", "gemini-3-flash", `{}`, "gemini-3-flash"},
		{"unknown", "not-a-model", `{}`, "not-a-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveAntigravityEffortVariant(auth, tc.model, []byte(tc.payload), "openai"); got != tc.want {
				t.Fatalf("ResolveAntigravityEffortVariant(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

// The credential's registered models decide: an own name the credential did
// not register (its operator excluded it) is sent as it is, and an aliased
// variant is sent under the catalogue id it was made from.
func TestResolveAntigravityEffortVariantFollowsTheCredentialsRegistration(t *testing.T) {
	excluded := registerAntigravityEffortVariants(t, "helps-effort-variant-excluded", []*registry.ModelInfo{
		{ID: "gemini-4-hydrogen-xhigh", Thinking: &registry.ThinkingSupport{Levels: []string{"xhigh"}}},
	})
	if got := ResolveAntigravityEffortVariant(excluded, "claude-opus-5-5", []byte(`{}`), "openai"); got != "claude-opus-5-5" {
		t.Fatalf("an own name the credential did not register was mapped to %q", got)
	}

	aliased := registerAntigravityEffortVariants(t, "helps-effort-variant-aliased", []*registry.ModelInfo{
		{ID: "hydrogen-xhigh", MetadataModelID: "gemini-4-hydrogen-xhigh", Thinking: &registry.ThinkingSupport{Levels: []string{"xhigh"}}},
	})
	if got := ResolveAntigravityEffortVariant(aliased, "hydrogen", []byte(`{}`), "openai"); got != "gemini-4-hydrogen-xhigh" {
		t.Fatalf("aliased own name was mapped to %q, want the catalogue id", got)
	}

	if got := ResolveAntigravityEffortVariant(nil, "hydrogen", []byte(`{}`), "openai"); got != "hydrogen" {
		t.Fatalf("a request with no credential was mapped to %q", got)
	}
}
