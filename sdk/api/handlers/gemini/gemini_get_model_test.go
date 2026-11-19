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
