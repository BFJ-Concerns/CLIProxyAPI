package registry

import (
	"reflect"
	"testing"
)

func listedByKey(models []map[string]any, key string) map[string]map[string]any {
	byID := make(map[string]map[string]any, len(models))
	for _, model := range models {
		id, _ := model[key].(string)
		byID[id] = model
	}
	return byID
}

func describedModelRegistry() *ModelRegistry {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "codex", []*ModelInfo{
		{ID: "levels-model", OwnedBy: "openai", ContextLength: 272000, MaxCompletionTokens: 128000,
			Thinking:                 &ThinkingSupport{Levels: []string{"none", "low", "medium", "high", "xhigh"}},
			SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"text"}},
	})
	r.RegisterClient("client-2", "gemini", []*ModelInfo{
		{ID: "budget-model", Name: "budget-model", InputTokenLimit: 1048576, OutputTokenLimit: 65536,
			Thinking:                 &ThinkingSupport{Min: 128, Max: 32768, ZeroAllowed: true, DynamicAllowed: true},
			SupportedInputModalities: []string{"TEXT", "IMAGE", "AUDIO"}, SupportedOutputModalities: []string{"TEXT"}},
	})
	r.RegisterClient("client-3", "claude", []*ModelInfo{
		{ID: "plain-model", ContextLength: 128000, MaxCompletionTokens: 8192},
	})
	r.RegisterClient("client-4", "openai-compatibility", []*ModelInfo{
		{ID: "user-model", UserDefined: true},
	})
	return r
}

func TestOpenAIModelListDescribesEachModel(t *testing.T) {
	listed := listedByKey(describedModelRegistry().GetAvailableModels("openai"), "id")

	levels := listed["levels-model"]
	if levels["context_length"] != 272000 || levels["max_completion_tokens"] != 128000 {
		t.Fatalf("levels-model limits = %v", levels)
	}
	if !reflect.DeepEqual(levels["input_modalities"], []string{"text", "image"}) || !reflect.DeepEqual(levels["output_modalities"], []string{"text"}) {
		t.Fatalf("levels-model modalities = %v / %v", levels["input_modalities"], levels["output_modalities"])
	}
	wantLevels := map[string]any{"supported": true, "efforts": []string{"none", "low", "medium", "high", "xhigh"}, "can_disable": true}
	if !reflect.DeepEqual(levels["reasoning"], wantLevels) {
		t.Fatalf("levels-model reasoning = %#v, want %#v", levels["reasoning"], wantLevels)
	}

	budget := listed["budget-model"]
	if budget["input_token_limit"] != 1048576 || budget["output_token_limit"] != 65536 {
		t.Fatalf("budget-model limits = %v", budget)
	}
	if _, ok := budget["context_length"]; ok {
		t.Fatalf("budget-model has a context_length the registry does not hold: %v", budget)
	}
	wantBudget := map[string]any{"supported": true, "budget_tokens": map[string]any{"min": 128, "max": 32768}, "can_disable": true, "dynamic_budget": true}
	if !reflect.DeepEqual(budget["reasoning"], wantBudget) {
		t.Fatalf("budget-model reasoning = %#v, want %#v", budget["reasoning"], wantBudget)
	}

	plain := listed["plain-model"]
	if !reflect.DeepEqual(plain["reasoning"], map[string]any{"supported": false}) {
		t.Fatalf("plain-model reasoning = %#v", plain["reasoning"])
	}
	for _, key := range []string{"input_modalities", "output_modalities", "input_token_limit"} {
		if _, ok := plain[key]; ok {
			t.Fatalf("plain-model has %s the registry does not hold: %v", key, plain)
		}
	}

	if _, ok := listed["user-model"]["reasoning"]; ok {
		t.Fatalf("user-defined model claims reasoning support the registry does not know: %v", listed["user-model"])
	}
}

func TestAnthropicModelListDescribesEachModel(t *testing.T) {
	listed := listedByKey(describedModelRegistry().GetAvailableModels("claude"), "id")

	levels := listed["levels-model"]
	if levels["context_length"] != 272000 || levels["max_input_tokens"] != 272000 || levels["max_tokens"] != 128000 {
		t.Fatalf("levels-model limits = %v", levels)
	}
	if _, ok := levels["reasoning"].(map[string]any); !ok {
		t.Fatalf("levels-model reasoning = %#v", levels["reasoning"])
	}
	wantCapabilities := map[string]any{
		"thinking": map[string]any{"supported": true},
		"effort": map[string]any{
			"supported": true,
			"low":       map[string]any{"supported": true},
			"medium":    map[string]any{"supported": true},
			"high":      map[string]any{"supported": true},
			"xhigh":     map[string]any{"supported": true},
		},
		"image_input": map[string]any{"supported": true},
	}
	if !reflect.DeepEqual(levels["capabilities"], wantCapabilities) {
		t.Fatalf("levels-model capabilities = %#v, want %#v", levels["capabilities"], wantCapabilities)
	}

	budget := listed["budget-model"]["capabilities"].(map[string]any)
	if !reflect.DeepEqual(budget["effort"], map[string]any{"supported": false}) {
		t.Fatalf("budget-model effort = %#v", budget["effort"])
	}
	if !reflect.DeepEqual(budget["image_input"], map[string]any{"supported": true}) {
		t.Fatalf("budget-model image_input = %#v", budget["image_input"])
	}

	plain := listed["plain-model"]["capabilities"].(map[string]any)
	if _, ok := plain["image_input"]; ok {
		t.Fatalf("plain-model claims image input the registry does not hold: %#v", plain)
	}
	if _, ok := listed["user-model"]["capabilities"]; ok {
		t.Fatalf("user-defined model has capabilities the registry does not hold: %#v", listed["user-model"])
	}
}

func TestGeminiModelListDescribesEachModel(t *testing.T) {
	listed := listedByKey(describedModelRegistry().GetAvailableModels("gemini"), "name")

	budget := listed["budget-model"]
	if budget["thinking"] != true {
		t.Fatalf("budget-model thinking = %v", budget["thinking"])
	}
	wantBudget := map[string]any{"supported": true, "budgetTokens": map[string]any{"min": 128, "max": 32768}, "canDisable": true, "dynamicBudget": true}
	if !reflect.DeepEqual(budget["reasoning"], wantBudget) {
		t.Fatalf("budget-model reasoning = %#v, want %#v", budget["reasoning"], wantBudget)
	}
	if !reflect.DeepEqual(budget["supportedInputModalities"], []string{"TEXT", "IMAGE", "AUDIO"}) {
		t.Fatalf("budget-model modalities = %v", budget["supportedInputModalities"])
	}

	if listed["plain-model"]["thinking"] != false {
		t.Fatalf("plain-model thinking = %v", listed["plain-model"]["thinking"])
	}
	if _, ok := listed["user-model"]["thinking"]; ok {
		t.Fatalf("user-defined model has thinking the registry does not hold: %v", listed["user-model"])
	}
}
