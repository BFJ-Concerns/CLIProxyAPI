package gemini

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
)

// A model lookup finds an effort-named id the lists leave out, since requests
// naming it are still served, as well as the model's own name.
func TestGeminiGetHandlerFindsAnEffortNamedID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const clientID = "gemini-get-effort-variant-test"
	registryRef := registry.GetGlobalRegistry()
	registryRef.RegisterClient(clientID, "antigravity", registry.FoldEffortVariants([]*registry.ModelInfo{{
		ID: "lookup-model-xhigh", Thinking: &registry.ThinkingSupport{Levels: []string{"xhigh"}},
	}}))
	t.Cleanup(func() { registryRef.UnregisterClient(clientID) })

	router := gin.New()
	router.GET("/v1beta/models/*action", NewGeminiAPIHandler(&handlers.BaseAPIHandler{}).GeminiGetHandler)
	for _, id := range []string{"lookup-model", "lookup-model-xhigh"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1beta/models/"+id, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d; body=%s", id, recorder.Code, recorder.Body.String())
		}
		var model map[string]any
		if errUnmarshal := json.Unmarshal(recorder.Body.Bytes(), &model); errUnmarshal != nil {
			t.Fatalf("decode %s: %v", id, errUnmarshal)
		}
		if model["name"] != "models/"+id {
			t.Fatalf("GET %s = %v", id, model)
		}
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1beta/models/lookup-model-high", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("GET of an unregistered id status = %d", recorder.Code)
	}
}

func TestGeminiGetHandlerRequiresAnAvailableCredentialForAHiddenEffortNamedID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		modelID = "lookup-availability-model-xhigh"
		first   = "gemini-get-availability-first"
		second  = "gemini-get-availability-second"
	)
	registryRef := registry.GetGlobalRegistry()
	models := registry.FoldEffortVariants([]*registry.ModelInfo{{
		ID: modelID, Thinking: &registry.ThinkingSupport{Levels: []string{"xhigh"}},
	}})
	for _, clientID := range []string{first, second} {
		registryRef.RegisterClient(clientID, "antigravity", models)
		t.Cleanup(func() { registryRef.UnregisterClient(clientID) })
	}

	router := gin.New()
	router.GET("/v1beta/models/*action", NewGeminiAPIHandler(&handlers.BaseAPIHandler{}).GeminiGetHandler)
	assertStatus := func(want int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1beta/models/"+modelID, nil))
		if recorder.Code != want {
			t.Fatalf("GET %s status = %d, want %d. Body: %s", modelID, recorder.Code, want, recorder.Body.String())
		}
	}

	registryRef.SetModelQuotaExceeded(first, modelID)
	assertStatus(http.StatusOK)
	registryRef.SetModelQuotaExceeded(second, modelID)
	assertStatus(http.StatusNotFound)
	registryRef.ClearModelQuotaExceeded(second, modelID)
	registryRef.SuspendClientModel(second, modelID, "manual")
	assertStatus(http.StatusNotFound)
	registryRef.ClearModelQuotaExceeded(first, modelID)
	registryRef.SuspendClientModel(first, modelID, "manual")
	assertStatus(http.StatusNotFound)
	registryRef.ResumeClientModel(first, modelID)
	assertStatus(http.StatusOK)
}

func TestGeminiGetHandlerKeepsTheListedOwnNameThroughAQuotaCooldown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const modelID = "lookup-quota-model"
	registryRef := registry.GetGlobalRegistry()
	models := registry.FoldEffortVariants([]*registry.ModelInfo{{
		ID: modelID + "-xhigh", Thinking: &registry.ThinkingSupport{Levels: []string{"xhigh"}},
	}})
	for _, clientID := range []string{"gemini-get-quota-first", "gemini-get-quota-second"} {
		registryRef.RegisterClient(clientID, "antigravity", models)
		t.Cleanup(func() { registryRef.UnregisterClient(clientID) })
		registryRef.SetModelQuotaExceeded(clientID, modelID)
	}

	router := gin.New()
	router.GET("/v1beta/models/*action", NewGeminiAPIHandler(&handlers.BaseAPIHandler{}).GeminiGetHandler)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1beta/models/"+modelID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d. Body: %s", modelID, recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var model map[string]any
	if errUnmarshal := json.Unmarshal(recorder.Body.Bytes(), &model); errUnmarshal != nil {
		t.Fatalf("decode %s: %v", modelID, errUnmarshal)
	}
	if model["name"] != "models/"+modelID {
		t.Errorf("GET %s name = %v, want models/%s", modelID, model["name"], modelID)
	}

	// The handler checks the list first. Check the single-model lookup too.
	if got := registryRef.GetAvailableModel("gemini", modelID); got["name"] != modelID {
		t.Errorf("single-model lookup for %s = %v during quota cooldown", modelID, got)
	}
}
