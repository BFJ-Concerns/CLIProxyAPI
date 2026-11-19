package registry

import (
	"reflect"
	"testing"
)

func effortVariantCatalogue() []*ModelInfo {
	return []*ModelInfo{
		{ID: "claude-opus-5-5-high", Name: "claude-opus-5-5-high", DisplayName: "Claude Opus 5.5 (High)", Description: "Claude Opus 5.5 (High)", ContextLength: 1000000, MaxCompletionTokens: 128000,
			Thinking: &ThinkingSupport{Min: 1024, Max: 64000, ZeroAllowed: true, DynamicAllowed: true, Levels: []string{"low", "medium", "high"}}},
		{ID: "gemini-4-hydrogen-high", Thinking: &ThinkingSupport{Min: 1, Max: 32768, Levels: []string{"low", "medium", "high"}}},
		{ID: "gemini-4-hydrogen-xhigh", DisplayName: "Gemini 4 Hydrogen", Thinking: &ThinkingSupport{Min: 1, Max: 65535, DynamicAllowed: true, Levels: []string{"xhigh"}}},
		{ID: "gemini-3-flash", Thinking: &ThinkingSupport{Levels: []string{"minimal", "high"}}},
		{ID: "gpt-oss-120b-medium"},
		{ID: "claude-opus-4-6-thinking", Thinking: &ThinkingSupport{Min: 1024, Max: 64000}},
	}
}

func modelIDs(models []*ModelInfo) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestFoldEffortVariantsListsEachModelOnceWithEveryEffort(t *testing.T) {
	folded := FoldEffortVariants(effortVariantCatalogue())

	wantIDs := []string{
		"claude-opus-5-5", "claude-opus-5-5-high",
		"gemini-4-hydrogen", "gemini-4-hydrogen-high", "gemini-4-hydrogen-xhigh",
		"gemini-3-flash", "gpt-oss-120b-medium", "claude-opus-4-6-thinking",
	}
	if got := modelIDs(folded); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("folded ids = %v, want %v", got, wantIDs)
	}

	byID := make(map[string]*ModelInfo, len(folded))
	for _, model := range folded {
		byID[model.ID] = model
	}

	opus := byID["claude-opus-5-5"]
	if opus.Name != "claude-opus-5-5" || opus.DisplayName != "Claude Opus 5.5" || opus.Description != "Claude Opus 5.5" {
		t.Fatalf("opus naming = %q / %q / %q", opus.Name, opus.DisplayName, opus.Description)
	}
	if opus.ContextLength != 1000000 || opus.MaxCompletionTokens != 128000 {
		t.Fatalf("opus limits = %d / %d", opus.ContextLength, opus.MaxCompletionTokens)
	}
	if !reflect.DeepEqual(opus.Thinking.Levels, []string{"low", "medium", "high"}) || !opus.Thinking.ZeroAllowed {
		t.Fatalf("opus thinking = %+v", opus.Thinking)
	}

	hydrogen := byID["gemini-4-hydrogen"]
	if !reflect.DeepEqual(hydrogen.Thinking.Levels, []string{"low", "medium", "high", "xhigh"}) {
		t.Fatalf("hydrogen levels = %v, want the union of its variants", hydrogen.Thinking.Levels)
	}
	if hydrogen.Thinking.Min != 1 || hydrogen.Thinking.Max != 65535 || !hydrogen.Thinking.DynamicAllowed {
		t.Fatalf("hydrogen budget = %+v", hydrogen.Thinking)
	}
	if hydrogen.DisplayName != "Gemini 4 Hydrogen" {
		t.Fatalf("hydrogen display name = %q", hydrogen.DisplayName)
	}

	for _, id := range []string{"claude-opus-5-5-high", "gemini-4-hydrogen-high", "gemini-4-hydrogen-xhigh"} {
		if byID[id].EffortVariantOf == "" {
			t.Fatalf("%s is not marked as an effort variant", id)
		}
	}
	for _, id := range []string{"claude-opus-5-5", "gemini-4-hydrogen", "gemini-3-flash", "gpt-oss-120b-medium", "claude-opus-4-6-thinking"} {
		if byID[id].EffortVariantOf != "" {
			t.Fatalf("%s is marked as an effort variant of %q", id, byID[id].EffortVariantOf)
		}
	}
}

func TestFoldEffortVariantsKeepsAnEntryUnderTheModelsOwnName(t *testing.T) {
	folded := FoldEffortVariants([]*ModelInfo{
		{ID: "gemini-3-flash-high", Thinking: &ThinkingSupport{Levels: []string{"high"}}},
		{ID: "gemini-3-flash", DisplayName: "Gemini 3 Flash", Thinking: &ThinkingSupport{Levels: []string{"minimal", "low"}}},
	})
	if got := modelIDs(folded); !reflect.DeepEqual(got, []string{"gemini-3-flash-high", "gemini-3-flash"}) {
		t.Fatalf("folded ids = %v", got)
	}
	if got := folded[1].Thinking.Levels; !reflect.DeepEqual(got, []string{"minimal", "low"}) {
		t.Fatalf("own entry levels = %v, want the efforts the own entry serves", got)
	}
	if folded[0].EffortVariantOf != "gemini-3-flash" {
		t.Fatalf("variant marked as %q", folded[0].EffortVariantOf)
	}
}

func TestFoldEffortVariantsDoesNotMutateItsInput(t *testing.T) {
	catalogue := effortVariantCatalogue()
	FoldEffortVariants(catalogue)
	for _, model := range catalogue {
		if model.EffortVariantOf != "" {
			t.Fatalf("input entry %s was marked", model.ID)
		}
	}
}

func TestEffortVariantForChoosesTheIDThatServesTheEffort(t *testing.T) {
	catalogue := effortVariantCatalogue()
	cases := []struct {
		model, effort, want string
		ok                  bool
	}{
		{"claude-opus-5-5", "", "claude-opus-5-5-high", true},
		{"claude-opus-5-5", "low", "claude-opus-5-5-high", true},
		{"gemini-4-hydrogen", "high", "gemini-4-hydrogen-high", true},
		{"gemini-4-hydrogen", "medium", "gemini-4-hydrogen-high", true},
		{"gemini-4-hydrogen", "xhigh", "gemini-4-hydrogen-xhigh", true},
		{"gemini-4-hydrogen", "", "gemini-4-hydrogen-xhigh", true},
		{"GEMINI-4-HYDROGEN", "", "gemini-4-hydrogen-xhigh", true},
		{"claude-opus-5-5-high", "low", "", false},
		{"gemini-3-flash", "high", "", false},
		{"gpt-oss-120b", "", "", false},
		{"unknown", "high", "", false},
	}
	for _, tc := range cases {
		got, ok := EffortVariantFor(catalogue, tc.model, tc.effort)
		if got != tc.want || ok != tc.ok {
			t.Errorf("EffortVariantFor(%q, %q) = %q, %v; want %q, %v", tc.model, tc.effort, got, ok, tc.want, tc.ok)
		}
	}
}

func TestModelListsLeaveOutEffortVariantsThatStayRoutable(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("antigravity-1", "antigravity", FoldEffortVariants(effortVariantCatalogue()))

	for _, handlerType := range []string{"openai", "claude", "gemini"} {
		idKey := "id"
		if handlerType == "gemini" {
			idKey = "name"
		}
		listed := map[string]map[string]any{}
		for _, model := range r.GetAvailableModels(handlerType) {
			id, _ := model[idKey].(string)
			listed[id] = model
		}
		for _, hidden := range []string{"claude-opus-5-5-high", "gemini-4-hydrogen-high", "gemini-4-hydrogen-xhigh"} {
			if _, ok := listed[hidden]; ok {
				t.Fatalf("%s list shows effort variant %s", handlerType, hidden)
			}
		}
		for _, shown := range []string{"claude-opus-5-5", "gemini-4-hydrogen", "gemini-3-flash", "gpt-oss-120b-medium"} {
			if _, ok := listed[shown]; !ok {
				t.Fatalf("%s list is missing %s: %v", handlerType, shown, listed)
			}
		}
	}

	infos := map[string]bool{}
	for _, info := range r.GetAvailableModelInfos() {
		infos[info.ID] = true
	}
	if infos["claude-opus-5-5-high"] || !infos["claude-opus-5-5"] {
		t.Fatalf("available model infos = %v", infos)
	}

	for _, id := range []string{"claude-opus-5-5-high", "gemini-4-hydrogen-xhigh", "claude-opus-5-5"} {
		if providers := r.GetModelProviders(id); len(providers) == 0 {
			t.Fatalf("%s has no provider to route to", id)
		}
	}
}

func TestModelListShowsAnIDOneProviderNamesForAnEffortAndAnotherDoesNot(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("antigravity-1", "antigravity", FoldEffortVariants([]*ModelInfo{
		{ID: "shared-high", Thinking: &ThinkingSupport{Levels: []string{"high"}}},
	}))
	r.RegisterClient("other-1", "other", []*ModelInfo{{ID: "shared-high"}})

	found := false
	for _, model := range r.GetAvailableModels("openai") {
		if model["id"] == "shared-high" {
			found = true
		}
	}
	if !found {
		t.Fatal("a model another provider lists under its own id was hidden")
	}
}

func TestEffortVariantForLeavesAnOwnNameListedAfterItsVariants(t *testing.T) {
	catalogue := []*ModelInfo{
		{ID: "claude-opus-5-5-high", Thinking: &ThinkingSupport{Levels: []string{"low", "medium", "high"}}},
		{ID: "claude-opus-5-5", Thinking: &ThinkingSupport{Levels: []string{"low", "medium", "high"}}},
	}
	for _, effort := range []string{"", "high", "low"} {
		if got, ok := EffortVariantFor(catalogue, "claude-opus-5-5", effort); ok {
			t.Fatalf("EffortVariantFor(claude-opus-5-5, %q) = %q, want the own name kept", effort, got)
		}
	}
}

func TestFoldEffortVariantsUnlessLeavesAnExcludedOwnNamesVariantsListed(t *testing.T) {
	folded := FoldEffortVariantsUnless(effortVariantCatalogue(), func(ownName string) bool {
		return ownName == "claude-opus-5-5"
	})

	byID := make(map[string]*ModelInfo, len(folded))
	for _, model := range folded {
		byID[model.ID] = model
	}
	if _, ok := byID["claude-opus-5-5"]; ok {
		t.Fatal("an excluded own name was made")
	}
	if byID["claude-opus-5-5-high"] == nil || byID["claude-opus-5-5-high"].EffortVariantOf != "" {
		t.Fatalf("claude-opus-5-5-high = %v, want it listed as it is", byID["claude-opus-5-5-high"])
	}
	if byID["gemini-4-hydrogen"] == nil || !byID["gemini-4-hydrogen"].EffortFolded {
		t.Fatalf("gemini-4-hydrogen = %v", byID["gemini-4-hydrogen"])
	}
}

func TestFoldEffortVariantsMarksOnlyTheEntriesItMakes(t *testing.T) {
	catalogue := []*ModelInfo{
		{ID: "claude-opus-5-5", Thinking: &ThinkingSupport{Levels: []string{"low", "high"}}},
		{ID: "claude-opus-5-5-high", Thinking: &ThinkingSupport{Levels: []string{"low", "high"}}},
		{ID: "gemini-4-hydrogen-xhigh", Thinking: &ThinkingSupport{Levels: []string{"xhigh"}}},
	}
	for _, model := range FoldEffortVariants(catalogue) {
		if want := model.ID == "gemini-4-hydrogen"; model.EffortFolded != want {
			t.Fatalf("%s EffortFolded = %v, want %v", model.ID, model.EffortFolded, want)
		}
	}
}

func TestRegisteredEffortVariantForDecidesFromTheRegisteredModels(t *testing.T) {
	full := FoldEffortVariants(effortVariantCatalogue())
	withoutXHigh := FoldEffortVariants(effortVariantCatalogue()[:2])
	aliased := FoldEffortVariants([]*ModelInfo{
		{ID: "hydrogen-high", MetadataModelID: "gemini-4-hydrogen-high", Thinking: &ThinkingSupport{Levels: []string{"low", "medium", "high"}}},
		{ID: "hydrogen-xhigh", MetadataModelID: "gemini-4-hydrogen-xhigh", Thinking: &ThinkingSupport{Levels: []string{"xhigh"}}},
	})
	ownNameListed := FoldEffortVariants([]*ModelInfo{
		{ID: "claude-opus-5-5", Thinking: &ThinkingSupport{Levels: []string{"low", "high"}}},
		{ID: "claude-opus-5-5-high", Thinking: &ThinkingSupport{Levels: []string{"low", "high"}}},
	})
	prefixed := FoldEffortVariants([]*ModelInfo{
		{ID: "team/gemini-4-hydrogen-xhigh", MetadataModelID: "gemini-4-hydrogen-xhigh", Thinking: &ThinkingSupport{Levels: []string{"xhigh"}}},
		{ID: "gemini-4-hydrogen-high", Thinking: &ThinkingSupport{Levels: []string{"high"}}},
	})
	cases := []struct {
		name                string
		registered          []*ModelInfo
		model, effort, want string
		ok                  bool
	}{
		{"named effort", full, "gemini-4-hydrogen", "xhigh", "gemini-4-hydrogen-xhigh", true},
		{"accepting effort", full, "gemini-4-hydrogen", "medium", "gemini-4-hydrogen-high", true},
		{"no effort", full, "gemini-4-hydrogen", "", "gemini-4-hydrogen-xhigh", true},
		{"case-insensitive", full, "GEMINI-4-HYDROGEN", "", "gemini-4-hydrogen-xhigh", true},
		{"excluded variant", withoutXHigh, "gemini-4-hydrogen", "xhigh", "gemini-4-hydrogen-high", true},
		{"aliased variant", aliased, "hydrogen", "xhigh", "gemini-4-hydrogen-xhigh", true},
		{"aliased variant at another effort", aliased, "hydrogen", "low", "gemini-4-hydrogen-high", true},
		{"own name listed", ownNameListed, "claude-opus-5-5", "high", "", false},
		{"effort-named id", full, "gemini-4-hydrogen-high", "xhigh", "", false},
		{"not registered", withoutXHigh, "gemini-4-argon", "", "", false},
		{"prefixed copy is not a variant", prefixed, "gemini-4-hydrogen", "xhigh", "gemini-4-hydrogen-high", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RegisteredEffortVariantFor(tc.registered, tc.model, tc.effort)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("RegisteredEffortVariantFor(%q, %q) = %q, %v; want %q, %v", tc.model, tc.effort, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestGetAvailableModelFindsAnEffortNamedIDTheListsLeaveOut(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("antigravity-lookup", "antigravity", FoldEffortVariants(effortVariantCatalogue()))

	for _, handlerType := range []string{"openai", "claude", "gemini"} {
		for _, id := range []string{"gemini-4-hydrogen-xhigh", "gemini-4-hydrogen"} {
			model := r.GetAvailableModel(handlerType, id)
			if model == nil {
				t.Fatalf("%s lookup of %s found nothing", handlerType, id)
			}
		}
	}
	if model := r.GetAvailableModel("gemini", "gemini-4-hydrogen-xhigh"); model["reasoning"] == nil {
		t.Fatalf("lookup entry is not described: %v", model)
	}
	if model := r.GetAvailableModel("gemini", "not-registered"); model != nil {
		t.Fatalf("lookup of an unregistered id = %v", model)
	}
}
