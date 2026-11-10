package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

// A `claude --bg` session sends X-App: cli-bg. It must reach upstream under its
// own identity rather than the cloaked baseline fingerprint, whose older
// version upstream refuses for newer models.
func TestClaudeExecutor_BackgroundSessionPreservesOwnIdentity(t *testing.T) {
	var seenBody []byte
	var seenHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		seenHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-fable-5-2","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	const userAgent = "claude-cli/2.1.321 (external, cli)"
	const userID = `{"account_uuid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","device_id":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","session_id":"11111111-2222-4333-8444-555555555555"}`
	const billingHeader = "x-anthropic-billing-header: cc_version=2.1.321.a1b; cc_entrypoint=cli;"
	payload := []byte(`{"model":"claude-fable-5-2","max_tokens":1024,"system":[{"type":"text","text":` + fmt.Sprintf("%q", billingHeader) + `},{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}},{"type":"text","text":"background-system","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}],"metadata":{"user_id":` + fmt.Sprintf("%q", userID) + `}}`)
	incoming := http.Header{
		"User-Agent":     {userAgent},
		"X-App":          {"cli-bg"},
		"Anthropic-Beta": {"claude-code-20250219,interleaved-thinking-2025-05-14"},
		"Anthropic-Dangerous-Direct-Browser-Access": {"true"},
		"X-Claude-Code-Session-Id":                  {"11111111-2222-4333-8444-555555555555"},
		"X-Stainless-Package-Version":               {"0.128.0"},
		"X-Stainless-Runtime-Version":               {"v26.3.0"},
		"X-Stainless-Os":                            {"Linux"},
		"X-Stainless-Arch":                          {"x64"},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-background-session-oauth",
		Attributes: map[string]string{
			"api_key":  "sk-ant-oat-background-session",
			"base_url": server.URL,
		},
		Metadata: claudeOAuthTestMetadata(),
	}

	_, errExecute := NewClaudeExecutor(&config.Config{}).Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-fable-5-2",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatClaude,
		OriginalRequest: payload,
		Headers:         incoming,
	})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}

	assertClaudeFingerprint(t, seenHeaders, userAgent, "0.128.0", "v26.3.0", "Linux", "x64")
	if got := seenHeaders.Get("X-App"); got != "cli-bg" {
		t.Fatalf("X-App = %q, want the background session's own cli-bg", got)
	}
	if got := gjson.GetBytes(seenBody, "system.0.text").String(); !strings.HasPrefix(got, "x-anthropic-billing-header: cc_version=2.1.321.a1b; cc_entrypoint=cli;") {
		t.Fatalf("system.0.text = %q, want the background session's own billing header", got)
	}
	if got := gjson.GetBytes(seenBody, "system.2.text").String(); got != "background-system" {
		t.Fatalf("system.2.text = %q, want background session system preserved", got)
	}
}
