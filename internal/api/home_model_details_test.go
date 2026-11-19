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
