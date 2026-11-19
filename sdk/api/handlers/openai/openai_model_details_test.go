package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestOpenAIModelsDescribesEachModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	clientID := "openai-models-details-test"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "antigravity", registry.FoldEffortVariants([]*registry.ModelInfo{
		{ID: "details-model-xhigh", OwnedBy: "antigravity", ContextLength: 1048576, MaxCompletionTokens: 65536, MaxContextLength: 999,
			Thinking:                 &registry.ThinkingSupport{Min: 1, Max: 65535, Levels: []string{"low", "medium", "high", "xhigh"}},
			SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"text"}},
	}))
	t.Cleanup(func() { modelRegistry.UnregisterClient(clientID) })

	h := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	router := gin.New()
	router.GET("/v1/models", h.OpenAIModels)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data []map[string]any `json:"data"`
	}
	if errUnmarshal := json.Unmarshal(recorder.Body.Bytes(), &body); errUnmarshal != nil {
		t.Fatalf("decode: %v", errUnmarshal)
	}
	var entry map[string]any
	for _, model := range body.Data {
		switch model["id"] {
		case "details-model":
			entry = model
		case "details-model-xhigh":
			t.Fatalf("effort variant listed beside its model: %v", model)
		}
	}
	if entry == nil {
		t.Fatalf("details-model missing from %s", recorder.Body.String())
	}
	if entry["context_length"] != float64(1048576) || entry["max_completion_tokens"] != float64(65536) {
		t.Fatalf("limits = %v", entry)
	}
	if _, ok := entry["max_context_length"]; ok {
		t.Fatalf("internal max_context_length exposed: %v", entry)
	}
	reasoning, _ := entry["reasoning"].(map[string]any)
	efforts, _ := reasoning["efforts"].([]any)
	if reasoning["supported"] != true || len(efforts) != 4 || efforts[3] != "xhigh" {
		t.Fatalf("reasoning = %v", entry["reasoning"])
	}
	if modalities, _ := entry["input_modalities"].([]any); len(modalities) != 2 {
		t.Fatalf("input_modalities = %v", entry["input_modalities"])
	}
}

// The handler copies exactly the description fields the registry writes into
// an OpenAI-shaped entry, so a field added to one side shows here.
func TestModelListDetailKeysMatchTheFieldsTheRegistryWrites(t *testing.T) {
	entry := map[string]any{}
	registry.AddModelListDetails(entry, &registry.ModelInfo{
		ContextLength: 1, MaxCompletionTokens: 1, InputTokenLimit: 1, OutputTokenLimit: 1,
		Thinking:                 &registry.ThinkingSupport{Min: 1, Max: 2, ZeroAllowed: true, DynamicAllowed: true, Levels: []string{"low"}},
		SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"text"},
	}, "openai", false)

	written := make(map[string]bool, len(entry))
	for key := range entry {
		written[key] = true
	}
	copied := make(map[string]bool, len(modelListDetailKeys))
	for _, key := range modelListDetailKeys {
		copied[key] = true
	}
	for key := range written {
		if !copied[key] {
			t.Errorf("the registry writes %q but the handler does not copy it", key)
		}
	}
	for key := range copied {
		if !written[key] {
			t.Errorf("the handler copies %q but the registry does not write it", key)
		}
	}
}
