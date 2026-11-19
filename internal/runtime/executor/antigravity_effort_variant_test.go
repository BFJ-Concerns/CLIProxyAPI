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
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// A request naming a model by its own name reaches Antigravity under the
// catalogue id that serves it, at the effort the request asks for.
func TestAntigravityExecutorSendsTheCatalogueIDForAModelsOwnName(t *testing.T) {
	sseResponse := "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}}\n\n"
	cases := []struct {
		model, wantModel, level string
	}{
		{"claude-opus-5-5", "claude-opus-5-5-high", "low"},
		{"gemini-4-hydrogen", "gemini-4-hydrogen-xhigh", "medium"},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			name := tc.model + "/non_stream"
			if stream {
				name = tc.model + "/stream"
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
				auth := &cliproxyauth.Auth{
					ID:       "ag-effort-variant-test",
					Provider: "antigravity",
					Metadata: map[string]any{
						"access_token": "token",
						"project_id":   "project-1",
						"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
					},
				}
				payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"Reply OK"}]}],"generationConfig":{"thinkingConfig":{"includeThoughts":true,"thinkingLevel":"` + tc.level + `"}}}`)
				req := cliproxyexecutor.Request{Model: tc.model, Payload: payload}
				opts := cliproxyexecutor.Options{
					SourceFormat:    sdktranslator.FormatGemini,
					ResponseFormat:  sdktranslator.FormatGemini,
					OriginalRequest: payload,
					Stream:          stream,
				}

				if stream {
					res, errStream := executor.ExecuteStream(ctx, auth, req, opts)
					if errStream != nil {
						t.Fatalf("ExecuteStream() error = %v", errStream)
					}
					for chunk := range res.Chunks {
						if chunk.Err != nil {
							t.Fatalf("stream chunk error = %v", chunk.Err)
						}
					}
				} else if _, errExec := executor.Execute(ctx, auth, req, opts); errExec != nil {
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
