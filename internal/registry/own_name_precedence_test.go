package registry

import (
	"reflect"
	"testing"
)

func claudeOwnNames() []*ModelInfo {
	return []*ModelInfo{
		{ID: "claude-opus-5-5", OwnedBy: "anthropic", Type: "claude", DisplayName: "Claude Opus 5.5", ContextLength: 200000, MaxCompletionTokens: 64000,
			Thinking: &ThinkingSupport{Levels: []string{"low", "medium", "high", "xhigh", "max"}}},
	}
}

func antigravityCatalogue() []*ModelInfo {
	models := effortVariantCatalogue()
	for _, model := range models {
		model.OwnedBy = "antigravity"
		model.Type = "antigravity"
	}
	return FoldEffortVariants(models)
}

func listEntry(t *testing.T, r *ModelRegistry, handlerType, id string) map[string]any {
	t.Helper()
	idKey := "id"
	if handlerType == "gemini" {
		idKey = "name"
	}
	var found map[string]any
	for _, model := range r.GetAvailableModels(handlerType) {
		if listed, _ := model[idKey].(string); listed == id || listed == "models/"+id {
			if found != nil {
				t.Fatalf("%s list shows %s twice", handlerType, id)
			}
			found = model
		}
	}
	return found
}

// assertOwnNameWithClaude checks that claude-opus-5-5 is routed, listed and
// described only by the Claude credential, while Antigravity's effort-named
// copy stays requestable and the names only Antigravity serves stay folded.
func assertOwnNameWithClaude(t *testing.T, r *ModelRegistry) {
	t.Helper()
	if r.ClientSupportsModel("antigravity-1", "claude-opus-5-5") {
		t.Fatal("the Antigravity credential is a candidate for a name the Claude credential serves")
	}
	if !r.ClientSupportsModel("claude-1", "claude-opus-5-5") {
		t.Fatal("the Claude credential no longer serves its own name")
	}
	if providers := r.GetModelProviders("claude-opus-5-5"); !reflect.DeepEqual(providers, []string{"claude"}) {
		t.Fatalf("providers for claude-opus-5-5 = %v, want only claude", providers)
	}
	for _, info := range r.GetModelsForClient("antigravity-1") {
		if info.ID == "claude-opus-5-5" {
			t.Fatal("the Antigravity credential's models include the Claude credential's own name")
		}
	}
	if !r.ClientSupportsModel("antigravity-1", "claude-opus-5-5-high") {
		t.Fatal("Antigravity's effort-named copy is no longer served")
	}
	if !r.ClientSupportsModel("antigravity-1", "gemini-4-hydrogen") {
		t.Fatal("a name only Antigravity serves is no longer folded")
	}

	claudeOnly := newTestModelRegistry()
	claudeOnly.RegisterClient("claude-1", "claude", claudeOwnNames())
	for _, handlerType := range []string{"openai", "claude", "gemini"} {
		got := listEntry(t, r, handlerType, "claude-opus-5-5")
		want := listEntry(t, claudeOnly, handlerType, "claude-opus-5-5")
		if got == nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s list entry for claude-opus-5-5 = %v, want the Claude credential's own %v", handlerType, got, want)
		}
		if variant := listEntry(t, r, handlerType, "claude-opus-5-5-high"); variant != nil {
			t.Fatalf("%s list shows the effort-named id beside the own name: %v", handlerType, variant)
		}
		if listEntry(t, r, handlerType, "gemini-4-hydrogen") == nil {
			t.Fatalf("%s list is missing the folded gemini-4-hydrogen", handlerType)
		}
	}
	if info := r.GetModelInfo("claude-opus-5-5", ""); info == nil || info.OwnedBy != "anthropic" || info.EffortFolded {
		t.Fatalf("model info for claude-opus-5-5 = %+v, want the Claude credential's", info)
	}
}

func assertOwnNameWithAntigravity(t *testing.T, r *ModelRegistry) {
	t.Helper()
	if !r.ClientSupportsModel("antigravity-1", "claude-opus-5-5") {
		t.Fatal("with no other credential serving claude-opus-5-5, Antigravity's copy is not folded into it")
	}
	if providers := r.GetModelProviders("claude-opus-5-5"); !reflect.DeepEqual(providers, []string{"antigravity"}) {
		t.Fatalf("providers for claude-opus-5-5 = %v, want antigravity", providers)
	}
	if entry := listEntry(t, r, "openai", "claude-opus-5-5"); entry == nil || entry["owned_by"] != "antigravity" {
		t.Fatalf("openai list entry for claude-opus-5-5 = %v, want Antigravity's folded entry", entry)
	}
	if listEntry(t, r, "openai", "claude-opus-5-5-high") != nil {
		t.Fatal("the effort-named id is listed beside the folded name")
	}
}

func TestOwnNameStaysWithTheProviderThatServesItInEitherRegistrationOrder(t *testing.T) {
	t.Run("antigravity first", func(t *testing.T) {
		r := newTestModelRegistry()
		r.RegisterClient("antigravity-1", "antigravity", antigravityCatalogue())
		r.RegisterClient("claude-1", "claude", claudeOwnNames())
		assertOwnNameWithClaude(t, r)
	})
	t.Run("claude first", func(t *testing.T) {
		r := newTestModelRegistry()
		r.RegisterClient("claude-1", "claude", claudeOwnNames())
		r.RegisterClient("antigravity-1", "antigravity", antigravityCatalogue())
		assertOwnNameWithClaude(t, r)
	})
}

func TestOwnNameFoldsIntoAntigravityOnlyWhileNoOtherCredentialServesIt(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("claude-1", "claude", claudeOwnNames())
	r.RegisterClient("claude-2", "claude", claudeOwnNames())
	r.RegisterClient("antigravity-1", "antigravity", antigravityCatalogue())
	assertOwnNameWithClaude(t, r)

	r.UnregisterClient("claude-2")
	assertOwnNameWithClaude(t, r)

	r.UnregisterClient("claude-1")
	assertOwnNameWithAntigravity(t, r)

	r.RegisterClient("claude-1", "claude", claudeOwnNames())
	assertOwnNameWithClaude(t, r)

	// A credential that drops the name from its models stops serving it.
	r.RegisterClient("claude-1", "claude", []*ModelInfo{{ID: "claude-haiku-5-5", OwnedBy: "anthropic"}})
	assertOwnNameWithAntigravity(t, r)
}

func TestOwnNameStaysWithItsProviderWhenThatProvidersCredentialsAreUnavailable(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("antigravity-1", "antigravity", antigravityCatalogue())
	r.RegisterClient("claude-1", "claude", claudeOwnNames())
	r.SetModelQuotaExceeded("claude-1", "claude-opus-5-5")
	r.SuspendClientModel("claude-1", "claude-opus-5-5", "quota")

	if r.ClientSupportsModel("antigravity-1", "claude-opus-5-5") {
		t.Fatal("a benched Claude credential hands its own name to Antigravity")
	}
	if providers := r.GetModelProviders("claude-opus-5-5"); !reflect.DeepEqual(providers, []string{"claude"}) {
		t.Fatalf("providers for claude-opus-5-5 = %v, want only claude", providers)
	}
}

func TestReregisteringAntigravityKeepsItsCopyHeldBack(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("claude-1", "claude", claudeOwnNames())
	r.RegisterClient("antigravity-1", "antigravity", antigravityCatalogue())
	epoch := r.ClientRegistrationEpoch("antigravity-1")
	if _, ok := r.ReplaceClientModels("antigravity-1", "antigravity", epoch, antigravityCatalogue()); !ok {
		t.Fatal("replacing the Antigravity credential's models failed")
	}
	assertOwnNameWithClaude(t, r)

	r.UnregisterClient("claude-1")
	assertOwnNameWithAntigravity(t, r)
}

func TestHandingTheOwnNameBackKeepsTheAntigravityCredentialsOtherState(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("antigravity-1", "antigravity", antigravityCatalogue())
	r.SuspendClientModel("antigravity-1", "gemini-4-hydrogen", "quota")
	r.RegisterClient("claude-1", "claude", claudeOwnNames())

	if r.ClientSupportsModel("antigravity-1", "claude-opus-5-5") {
		t.Fatal("the own name was not held back from the Antigravity credential")
	}
	if !r.IsModelSuspendedForClient("antigravity-1", "gemini-4-hydrogen") {
		t.Fatal("holding back the own name dropped a suspension on another of the credential's models")
	}
	r.UnregisterClient("claude-1")
	if !r.ClientSupportsModel("antigravity-1", "claude-opus-5-5") {
		t.Fatal("the own name was not handed back to the Antigravity credential")
	}
	if !r.IsModelSuspendedForClient("antigravity-1", "gemini-4-hydrogen") {
		t.Fatal("handing the own name back dropped a suspension on another of the credential's models")
	}
}

func TestAnAntigravityAliasDoesNotKeepAnotherAntigravityFoldFromReturning(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("claude-1", "claude", claudeOwnNames())
	r.RegisterClient("antigravity-alias", "antigravity", []*ModelInfo{
		{ID: "claude-opus-5-5", MetadataModelID: "claude-opus-5-5-high", OwnedBy: "antigravity"},
	})
	r.RegisterClient("antigravity-1", "antigravity", antigravityCatalogue())
	if r.ClientSupportsModel("antigravity-1", "claude-opus-5-5") {
		t.Fatal("the fold took the name while the Claude credential serves it")
	}

	r.UnregisterClient("claude-1")
	if !r.ClientSupportsModel("antigravity-1", "claude-opus-5-5") {
		t.Fatal("another Antigravity credential's alias kept the fold from returning once no other provider serves the name")
	}
	if !r.ClientSupportsModel("antigravity-alias", "claude-opus-5-5") {
		t.Fatal("the aliased credential stopped serving its name")
	}
}
