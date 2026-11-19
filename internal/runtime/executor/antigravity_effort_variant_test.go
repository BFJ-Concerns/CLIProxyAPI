package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// registerAntigravityEffortVariantAuth registers, for a new Antigravity
// credential, its catalogue's effort-named ids folded under their own names,
// as the service registers them.
func registerAntigravityEffortVariantAuth(t *testing.T, authID string, ids ...string) *cliproxyauth.Auth {
	t.Helper()
	catalogue := make(map[string]*registry.ModelInfo)
	for _, info := range registry.GetAntigravityModels() {
		catalogue[info.ID] = info
	}
	models := make([]*registry.ModelInfo, 0, len(ids))
	for _, id := range ids {
		info := catalogue[id]
		if info == nil {
			t.Fatalf("%s is not in the Antigravity catalogue", id)
		}
		models = append(models, info)
	}
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(authID, "antigravity", registry.FoldEffortVariants(models))
	t.Cleanup(func() { modelRegistry.UnregisterClient(authID) })
	return &cliproxyauth.Auth{
		ID:       authID,
		Provider: "antigravity",
		Metadata: map[string]any{
			"access_token": "token",
			"project_id":   "project-1",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
}

// A request naming a model by its own name reaches Antigravity under the
// catalogue id that serves it, at the effort the request asks for.
func TestAntigravityExecutorSendsTheCatalogueIDForAModelsOwnName(t *testing.T) {
	auth := registerAntigravityEffortVariantAuth(t, "ag-effort-variant-test", "claude-opus-5-5-high", "gemini-4-hydrogen-xhigh")
	unregistered := auth.Clone()
	unregistered.ID = "ag-effort-variant-home"
	sseResponse := "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}}\n\n"
	cases := []struct {
		name, model, wantModel, level string
		auth                          *cliproxyauth.Auth
	}{
		{"registered Claude", "claude-opus-5-5", "claude-opus-5-5-high", "low", auth},
		{"registered Gemini", "gemini-4-hydrogen", "gemini-4-hydrogen-xhigh", "medium", auth},
		{"unregistered Gemini", "gemini-4-hydrogen", "gemini-4-hydrogen-xhigh", "xhigh", unregistered},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			name := tc.name + "/non_stream"
			if stream {
				name = tc.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				var capturedBody []byte
				ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					raw, errRead := io.ReadAll(req.Body)
					if errRead != nil {
						return nil, errRead
					}
					if errClose := req.Body.Close(); errClose != nil {
						return nil, errClose
					}
					capturedBody = raw
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(sseResponse)),
						Request:    req,
					}, nil
				}))

				executor := NewAntigravityExecutor(&config.Config{})
				payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"Reply OK"}]}],"generationConfig":{"thinkingConfig":{"includeThoughts":true,"thinkingLevel":"` + tc.level + `"}}}`)
				req := cliproxyexecutor.Request{Model: tc.model, Payload: payload}
				opts := cliproxyexecutor.Options{
					SourceFormat:    sdktranslator.FormatGemini,
					ResponseFormat:  sdktranslator.FormatGemini,
					OriginalRequest: payload,
					Stream:          stream,
				}

				if stream {
					res, errStream := executor.ExecuteStream(ctx, tc.auth, req, opts)
					if errStream != nil {
						t.Fatalf("ExecuteStream() error = %v", errStream)
					}
					for chunk := range res.Chunks {
						if chunk.Err != nil {
							t.Fatalf("stream chunk error = %v", chunk.Err)
						}
					}
				} else if _, errExec := executor.Execute(ctx, tc.auth, req, opts); errExec != nil {
					t.Fatalf("Execute() error = %v", errExec)
				}

				if got := ojson.GetBytes(capturedBody, "model").String(); got != tc.wantModel {
					t.Fatalf("upstream model = %q, want %q; body=%s", got, tc.wantModel, capturedBody)
				}
				if got := ojson.GetBytes(capturedBody, "request.generationConfig.thinkingConfig.thinkingLevel").String(); got != tc.level {
					t.Fatalf("thinkingLevel = %q, want %q; body=%s", got, tc.level, capturedBody)
				}
			})
		}
	}
}

// A token count for a model's own name is decided for the catalogue id that
// serves it: the count request carries no model, so a payload rule written for
// that id shows which model the executor resolved.
func TestAntigravityExecutorCountsTokensForTheCatalogueIDOfAModelsOwnName(t *testing.T) {
	auth := registerAntigravityEffortVariantAuth(t, "ag-effort-variant-tokens", "gemini-4-hydrogen-xhigh")
	var capturedBody []byte
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		raw, errRead := io.ReadAll(req.Body)
		if errRead != nil {
			return nil, errRead
		}
		if errClose := req.Body.Close(); errClose != nil {
			return nil, errClose
		}
		capturedBody = raw
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"totalTokens":3}`)),
			Request:    req,
		}, nil
	}))
	executor := NewAntigravityExecutor(&config.Config{Payload: config.PayloadConfig{
		Override: []config.PayloadRule{{
			Models: []config.PayloadModelRule{{Name: "gemini-4-hydrogen-xhigh"}},
			Params: map[string]any{"generationConfig.temperature": 0.25},
		}},
	}})
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)

	if _, errCount := executor.CountTokens(ctx, auth, cliproxyexecutor.Request{Model: "gemini-4-hydrogen", Payload: payload}, cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FormatGemini,
		ResponseFormat: sdktranslator.FormatGemini,
	}); errCount != nil {
		t.Fatalf("CountTokens() error = %v", errCount)
	}

	if got := ojson.GetBytes(capturedBody, "request.generationConfig.temperature").Float(); got != 0.25 {
		t.Fatalf("the payload rule for gemini-4-hydrogen-xhigh was not applied; body=%s", capturedBody)
	}
}
