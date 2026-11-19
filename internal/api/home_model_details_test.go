package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/home"
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

func TestHomeGeminiModelListCarriesModalities(t *testing.T) {
	entries, errDecode := decodeHomeModels([]byte(`{
		"gemini": [{"id": "described", "supportedInputModalities": ["text", "image"], "supportedOutputModalities": ["text"]}]
	}`))
	if errDecode != nil {
		t.Fatalf("decodeHomeModels: %v", errDecode)
	}
	gemini := formatHomeGeminiModel(entries[0])
	if !reflect.DeepEqual(gemini["supportedInputModalities"], []string{"text", "image"}) || !reflect.DeepEqual(gemini["supportedOutputModalities"], []string{"text"}) {
		t.Fatalf("gemini entry = %v", gemini)
	}
}

func TestHomeModelListsShowEachModelOnceUnderItsOwnName(t *testing.T) {
	server := newTestServer(t)
	server.cfg.Home.Enabled = true
	server.cfg.ClaudeCode.DisableCloakingModelList = true
	engine := gin.New()
	engine.GET("/v1/models", server.unifiedModelsHandler(nil, nil))
	engine.GET("/v1beta/models", server.geminiModelsHandler(nil))
	engine.GET("/v1beta/models/*action", server.geminiGetHandler(nil))

	client := newHomeCatalogClient(t, `{
		"antigravity": [
			{"id": "claude-opus-5-5-high", "display_name": "Claude Opus 5.5 (High)", "context_length": 1000000, "thinking": {"levels": ["low", "high"]}},
			{"id": "gemini-4-argon-high", "thinking": {"levels": ["low", "high"]}}
		],
		"gemini": [{"id": "gemini-4-argon", "thinking": {"levels": ["low", "medium", "high"]}}]
	}`)
	previousHome := home.Current()
	home.SetCurrent(client)
	t.Cleanup(func() { home.SetCurrent(previousHome) })

	get := func(path string, header map[string]string) []byte {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		for key, value := range header {
			request.Header.Set(key, value)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d; body=%s", path, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.Bytes()
	}
	listed := func(body []byte, listKey, idKey string) map[string]map[string]any {
		var payload map[string]any
		if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil {
			t.Fatalf("decode %s: %v", body, errUnmarshal)
		}
		out := map[string]map[string]any{}
		entries, _ := payload[listKey].([]any)
		for _, raw := range entries {
			model, _ := raw.(map[string]any)
			id, _ := model[idKey].(string)
			out[strings.TrimPrefix(id, "models/")] = model
		}
		return out
	}

	shapes := map[string]map[string]map[string]any{
		"openai":    listed(get("/v1/models", nil), "data", "id"),
		"anthropic": listed(get("/v1/models", map[string]string{"Anthropic-Version": "2023-06-01"}), "data", "id"),
		"gemini":    listed(get("/v1beta/models", nil), "models", "name"),
	}
	for shape, models := range shapes {
		if len(models) != 2 {
			t.Fatalf("%s list = %v, want claude-opus-5-5 and gemini-4-argon", shape, models)
		}
		opus, ok := models["claude-opus-5-5"]
		if !ok {
			t.Fatalf("%s list is missing claude-opus-5-5: %v", shape, models)
		}
		efforts := opus["reasoning"].(map[string]any)["efforts"]
		if !reflect.DeepEqual(efforts, []any{"low", "high"}) {
			t.Fatalf("%s claude-opus-5-5 efforts = %v", shape, efforts)
		}
		argon := models["gemini-4-argon"]["reasoning"].(map[string]any)["efforts"]
		if !reflect.DeepEqual(argon, []any{"low", "medium", "high"}) {
			t.Fatalf("%s gemini-4-argon efforts = %v, want the efforts of the entry Home serves under that name", shape, argon)
		}
	}
	if got := shapes["anthropic"]["claude-opus-5-5"]["display_name"]; got != "Claude Opus 5.5" {
		t.Fatalf("display_name = %v", got)
	}

	var single map[string]any
	if errUnmarshal := json.Unmarshal(get("/v1beta/models/claude-opus-5-5", nil), &single); errUnmarshal != nil {
		t.Fatalf("decode single model: %v", errUnmarshal)
	}
	if single["name"] != "models/claude-opus-5-5" {
		t.Fatalf("single model = %v", single)
	}
}

// A model Home serves under its own name as well as under an effort-named id
// is listed once, under its own name, even when no new entry is folded.
func TestHomeModelListsLeaveOutAnEffortNamedIDBesideItsOwnName(t *testing.T) {
	server := newTestServer(t)
	server.cfg.Home.Enabled = true
	server.cfg.ClaudeCode.DisableCloakingModelList = true
	engine := gin.New()
	engine.GET("/v1/models", server.unifiedModelsHandler(nil, nil))
	engine.GET("/v1beta/models", server.geminiModelsHandler(nil))

	client := newHomeCatalogClient(t, `{
		"antigravity": [{"id": "gemini-4-argon-high", "thinking": {"levels": ["high"]}}],
		"gemini": [{"id": "gemini-4-argon", "thinking": {"levels": ["low", "medium", "high"]}}]
	}`)
	previousHome := home.Current()
	home.SetCurrent(client)
	t.Cleanup(func() { home.SetCurrent(previousHome) })

	cases := []struct {
		shape, path, header, listKey, idKey string
	}{
		{"openai", "/v1/models", "", "data", "id"},
		{"anthropic", "/v1/models", "2023-06-01", "data", "id"},
		{"gemini", "/v1beta/models", "", "models", "name"},
	}
	for _, tc := range cases {
		request := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.header != "" {
			request.Header.Set("Anthropic-Version", tc.header)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", tc.shape, recorder.Code, recorder.Body.String())
		}
		var payload map[string]any
		if errUnmarshal := json.Unmarshal(recorder.Body.Bytes(), &payload); errUnmarshal != nil {
			t.Fatalf("%s decode: %v", tc.shape, errUnmarshal)
		}
		var ids []string
		entries, _ := payload[tc.listKey].([]any)
		for _, raw := range entries {
			model, _ := raw.(map[string]any)
			id, _ := model[tc.idKey].(string)
			ids = append(ids, strings.TrimPrefix(id, "models/"))
		}
		if !reflect.DeepEqual(ids, []string{"gemini-4-argon"}) {
			t.Fatalf("%s list = %v, want only gemini-4-argon", tc.shape, ids)
		}
	}
}
