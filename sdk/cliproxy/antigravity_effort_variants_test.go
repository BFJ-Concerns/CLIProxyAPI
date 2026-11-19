package cliproxy

import (
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

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

func TestAntigravityAliasedModelsKeepTheirConfiguredNames(t *testing.T) {
	s := &Service{}
	auth := &coreauth.Auth{ID: "ag-effort-alias", Provider: "antigravity"}
	hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{"claude-opus-5-5-high": {}}}
	cfg := &config.Config{}
	cfg.OAuthModelAlias = map[string][]config.OAuthModelAlias{
		"antigravity": {{Name: "claude-opus-5-5-high", Alias: "my-opus-high"}},
	}

	models := s.antigravityModelsForHintsWithConfig(auth, hints, cfg)

	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	if byID["my-opus-high"] == nil || byID["my-opus-high"].EffortVariantOf != "" {
		t.Fatalf("configured alias my-opus-high is not listed: %v", byID["my-opus-high"])
	}
	if _, ok := byID["my-opus"]; ok {
		t.Fatal("an alias was folded into a name nothing can resolve")
	}
}
