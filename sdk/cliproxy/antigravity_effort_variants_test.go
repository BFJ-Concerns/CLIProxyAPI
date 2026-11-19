package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestAntigravityHomeLocalRegistrationResolvesFoldedNames(t *testing.T) {
	cfg := &config.Config{
		OAuthExcludedModels: map[string][]string{"antigravity": {"claude-opus-5-5", "gemini-4-argon-high"}},
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"antigravity": {{Name: "gemini-4-neon-xhigh", Alias: "neon-xhigh"}},
		},
	}
	cfg.Home.Enabled = true
	s := &Service{cfg: cfg}
	auth := &coreauth.Auth{ID: "ag-home-local-effort", Provider: "antigravity"}
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(auth.ID) })
	if !s.registerModelsForAuth(t.Context(), auth) {
		t.Fatal("Home-mode Antigravity registration was superseded")
	}

	for _, tc := range []struct{ model, want string }{
		{"gemini-4-hydrogen", "gemini-4-hydrogen-xhigh"},
		{"neon", "gemini-4-neon-xhigh"},
		{"claude-opus-5-5", "claude-opus-5-5"},
		{"gemini-4-argon", "gemini-4-argon"},
	} {
		if got := helps.ResolveAntigravityEffortVariant(auth, tc.model, nil, "openai"); got != tc.want {
			t.Errorf("ResolveAntigravityEffortVariant(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}

func TestAntigravityHomeLocalRegistrationAppliesThePrefixOnce(t *testing.T) {
	cfg := &config.Config{}
	cfg.Home.Enabled = true
	cfg.ForceModelPrefix = true
	s := &Service{cfg: cfg}
	auth := &coreauth.Auth{ID: "ag-home-local-prefix", Provider: "antigravity", Prefix: "tenant"}
	reg := GlobalModelRegistry()
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
	if !s.registerModelsForAuth(t.Context(), auth) {
		t.Fatal("Home-mode Antigravity registration was superseded")
	}

	const modelID = "gemini-4-hydrogen"
	if !reg.ClientSupportsModel(auth.ID, "tenant/"+modelID) {
		t.Errorf("Home registration is missing tenant/%s", modelID)
	}
	for _, id := range []string{modelID, "tenant/tenant/" + modelID} {
		if reg.ClientSupportsModel(auth.ID, id) {
			t.Errorf("Home registration unexpectedly includes %s", id)
		}
	}
}

func TestAntigravityModelsAreRegisteredUnderTheirOwnNames(t *testing.T) {
	s := &Service{}
	auth := &coreauth.Auth{ID: "ag-effort-variants", Provider: "antigravity"}
	hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{
		"claude-opus-5-5-high":    {},
		"gemini-4-hydrogen-xhigh": {},
		"gemini-3-flash":          {},
	}}

	models := s.antigravityModelsForHintsWithConfig(auth, hints, &config.Config{})

	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	for base, variant := range map[string]string{"claude-opus-5-5": "claude-opus-5-5-high", "gemini-4-hydrogen": "gemini-4-hydrogen-xhigh"} {
		if byID[base] == nil || byID[base].EffortVariantOf != "" {
			t.Fatalf("%s is not registered as a listed model: %v", base, byID[base])
		}
		if byID[variant] == nil || byID[variant].EffortVariantOf != base {
			t.Fatalf("%s is not registered as an effort variant of %s: %v", variant, base, byID[variant])
		}
	}
	if byID["gemini-3-flash"] == nil || byID["gemini-3-flash"].EffortVariantOf != "" {
		t.Fatalf("gemini-3-flash = %v", byID["gemini-3-flash"])
	}
	if _, ok := byID["claude-sonnet-5-5"]; ok {
		t.Fatal("a model the account's catalogue does not offer was registered")
	}
}

func antigravityModelsByID(models []*ModelInfo) map[string]*ModelInfo {
	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	return byID
}

func TestAntigravityAliasedModelsAreFoldedUnderTheirConfiguredNames(t *testing.T) {
	s := &Service{}
	auth := &coreauth.Auth{ID: "ag-effort-alias", Provider: "antigravity"}
	hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{"gemini-4-hydrogen-xhigh": {}}}
	cfg := &config.Config{}
	cfg.OAuthModelAlias = map[string][]config.OAuthModelAlias{
		"antigravity": {{Name: "gemini-4-hydrogen-xhigh", Alias: "hydrogen-xhigh"}},
	}

	byID := antigravityModelsByID(s.antigravityModelsForHintsWithConfig(auth, hints, cfg))

	if byID["hydrogen"] == nil || !byID["hydrogen"].EffortFolded {
		t.Fatalf("the aliased variant is not folded under hydrogen: %v", byID["hydrogen"])
	}
	variant := byID["hydrogen-xhigh"]
	if variant == nil || variant.EffortVariantOf != "hydrogen" || variant.MetadataModelID != "gemini-4-hydrogen-xhigh" {
		t.Fatalf("hydrogen-xhigh is not registered as an effort variant of hydrogen made from the catalogue id: %v", variant)
	}
	if _, ok := byID["gemini-4-hydrogen"]; ok {
		t.Fatal("the catalogue's own name was registered though its variant is aliased")
	}
}

func TestAntigravityExcludedOwnNameIsNotRegistered(t *testing.T) {
	s := &Service{}
	auth := &coreauth.Auth{ID: "ag-effort-excluded-own-name", Provider: "antigravity"}
	hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{"claude-opus-5-5-high": {}, "gemini-4-hydrogen-xhigh": {}}}
	cfg := &config.Config{OAuthExcludedModels: map[string][]string{"antigravity": {"Claude-Opus-5-5"}}}

	byID := antigravityModelsByID(s.antigravityModelsForHintsWithConfig(auth, hints, cfg))

	if _, ok := byID["claude-opus-5-5"]; ok {
		t.Fatal("an excluded own name was registered")
	}
	if byID["claude-opus-5-5-high"] == nil || byID["claude-opus-5-5-high"].EffortVariantOf != "" {
		t.Fatalf("claude-opus-5-5-high is not listed as the catalogue has it: %v", byID["claude-opus-5-5-high"])
	}
	if byID["gemini-4-hydrogen"] == nil || !byID["gemini-4-hydrogen"].EffortFolded {
		t.Fatalf("a model not excluded lost its own name: %v", byID["gemini-4-hydrogen"])
	}
}

func TestAntigravityExcludedOwnNameMatchesWildcards(t *testing.T) {
	s := &Service{}
	auth := &coreauth.Auth{ID: "ag-effort-excluded-wildcard", Provider: "antigravity", Attributes: map[string]string{"excluded_models": "*opus-5-5"}}
	hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{"claude-opus-5-5-high": {}}}

	byID := antigravityModelsByID(s.antigravityModelsForHintsWithConfig(auth, hints, &config.Config{}))

	if _, ok := byID["claude-opus-5-5"]; ok {
		t.Fatal("an own name matched by a wildcard exclusion was registered")
	}
	if byID["claude-opus-5-5-high"] == nil || byID["claude-opus-5-5-high"].EffortVariantOf != "" {
		t.Fatalf("claude-opus-5-5-high = %v", byID["claude-opus-5-5-high"])
	}
}

func TestAntigravityExcludedVariantDoesNotMakeAnOwnName(t *testing.T) {
	s := &Service{}
	auth := &coreauth.Auth{ID: "ag-effort-excluded-variant", Provider: "antigravity"}
	hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{"gemini-4-hydrogen-xhigh": {}}}
	cfg := &config.Config{OAuthExcludedModels: map[string][]string{"antigravity": {"gemini-4-hydrogen-xhigh"}}}

	byID := antigravityModelsByID(s.antigravityModelsForHintsWithConfig(auth, hints, cfg))

	for _, id := range []string{"gemini-4-hydrogen-xhigh", "gemini-4-hydrogen"} {
		if _, ok := byID[id]; ok {
			t.Fatalf("%s was registered though its only variant is excluded", id)
		}
	}
}
