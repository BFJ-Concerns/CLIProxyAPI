package api

import (
	"reflect"
	"testing"
)

func TestHomeModelListsDescribeEachModel(t *testing.T) {
	entries, errDecode := decodeHomeModels([]byte(`{
		"codex": [
			{
				"id": "described",
				"context_length": 12345,
				"max_completion_tokens": 678,
				"thinking": {"levels": ["low", "high"]},
				"supportedInputModalities": ["text", "image"],
				"supportedOutputModalities": ["text"]
			},
			{"id": "unstated"}
		]
	}`))
	if errDecode != nil {
		t.Fatalf("decodeHomeModels: %v", errDecode)
	}
	byID := map[string]homeModelEntry{}
	for _, entry := range entries {
		byID[entry.id] = entry
	}

	openai := map[string]any{"id": "described", "object": "model"}
	addHomeModelListDetails(openai, byID["described"], "openai")
	if openai["context_length"] != 12345 || openai["max_completion_tokens"] != 678 {
		t.Fatalf("openai limits = %v", openai)
	}
	if !reflect.DeepEqual(openai["input_modalities"], []string{"text", "image"}) || !reflect.DeepEqual(openai["output_modalities"], []string{"text"}) {
		t.Fatalf("openai modalities = %v", openai)
	}
	reasoning, _ := openai["reasoning"].(map[string]any)
	if !reflect.DeepEqual(reasoning["efforts"], []string{"low", "high"}) {
		t.Fatalf("openai reasoning = %v", openai["reasoning"])
	}

	claude := formatHomeClaudeModel(byID["described"])
	if claude["context_length"] != 12345 || claude["reasoning"] == nil || claude["capabilities"] == nil {
		t.Fatalf("claude entry = %v", claude)
	}

	gemini := formatHomeGeminiModel(byID["described"])
	if gemini["thinking"] != true || gemini["reasoning"] == nil {
		t.Fatalf("gemini entry = %v", gemini)
	}

	unstated := map[string]any{"id": "unstated"}
	addHomeModelListDetails(unstated, byID["unstated"], "openai")
	if _, ok := unstated["reasoning"]; ok {
		t.Fatalf("a model Home says nothing about is described as reasoning or not: %v", unstated)
	}
}

func TestHomeModelListsShowEveryEffortAnyRouteAccepts(t *testing.T) {
	entries, errDecode := decodeHomeModels([]byte(`{
		"antigravity": [{"id": "shared-model", "thinking": {"levels": ["low", "high"]}}],
		"codex": [{"id": "shared-model", "thinking": {"levels": ["max"]}}]
	}`))
	if errDecode != nil {
		t.Fatalf("decodeHomeModels: %v", errDecode)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	want := []string{"low", "high", "max"}

	openai := map[string]any{"id": "shared-model"}
	addHomeModelListDetails(openai, entries[0], "openai")
	if got := openai["reasoning"].(map[string]any)["efforts"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("openai efforts = %v, want %v", got, want)
	}
	if got := formatHomeClaudeModel(entries[0])["reasoning"].(map[string]any)["efforts"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("claude efforts = %v, want %v", got, want)
	}
	if got := formatHomeGeminiModel(entries[0])["reasoning"].(map[string]any)["efforts"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("gemini efforts = %v, want %v", got, want)
	}

	claude := formatHomeClaudeModel(entries[0])
	for _, key := range []string{"max_input_tokens", "max_tokens"} {
		if got, ok := claude[key]; ok {
			t.Fatalf("Anthropic entry guesses %s = %v for a model Home gives no limit", key, got)
		}
	}
}
