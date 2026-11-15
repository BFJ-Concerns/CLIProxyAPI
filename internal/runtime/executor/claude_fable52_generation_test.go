package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestClaudeExecutor_Mythos52PreservedThinkingReplayInjectsDropBlock(t *testing.T) {
	const model = "claude-mythos-5-2"
	seenBody, seenHeaders := runClaudeExecutorCapture(t, nil, "", false, model, preservedThinkingReplayPayload(model))
	if got := ojson.GetBytes(seenBody, "thinking.block_binding.prefix_mismatch_behavior").String(); got != "drop_block" {
		t.Fatalf("thinking.block_binding.prefix_mismatch_behavior = %q, want drop_block; body=%s", got, seenBody)
	}
	if betas := seenHeaders.Get("Anthropic-Beta"); !strings.Contains(betas, claudeThinkingBindingBeta) {
		t.Fatalf("Anthropic-Beta = %q, want %s", betas, claudeThinkingBindingBeta)
	}
}

func TestApplyCloaking_Fable52GenerationAdditions(t *testing.T) {
	for _, tt := range []struct {
		model              string
		serverSideFallback bool
		wantFallback       string
	}{
		{model: "claude-fable-5-2", serverSideFallback: true, wantFallback: "claude-opus-5"},
		{model: "claude-fable-5-2"},
		{model: "claude-mythos-5-2", serverSideFallback: true},
	} {
		name := tt.model
		if tt.serverSideFallback {
			name += " with server-side fallback"
		}
		t.Run(name, func(t *testing.T) {
			const apiKey = "sk-ant-oat-fable52-test"
			cfg := &config.Config{
				Claude:    config.ClaudeConfig{ServerSideFallback: tt.serverSideFallback},
				ClaudeKey: []config.ClaudeKey{{APIKey: apiKey, Cloak: &config.CloakConfig{}}},
			}
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": apiKey}}
			payload := []byte(`{"model":"` + tt.model + `","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"test"}]}`)

			out, cloaked, err := applyCloaking(context.Background(), cfg, auth, payload, apiKey, false, true)
			if err != nil {
				t.Fatalf("applyCloaking() error = %v", err)
			}
			if !cloaked {
				t.Fatal("applyCloaking() cloaked = false, want true")
			}
			if got := ojson.GetBytes(out, "fallbacks.0.model").String(); got != tt.wantFallback {
				t.Fatalf("fallback model = %q, want %q; fallbacks=%s", got, tt.wantFallback, ojson.GetBytes(out, "fallbacks").Raw)
			}
			if got := ojson.GetBytes(out, "thinking.display").String(); got != "updates" {
				t.Fatalf("thinking.display = %q, want updates", got)
			}
			if betas := claudeCodeCLIBetas(out, nil, true); !strings.Contains(betas, claudePerTurnControlBeta) {
				t.Fatalf("betas = %q, want %s", betas, claudePerTurnControlBeta)
			}
		})
	}
}

func TestClaudeFable52FallbackSurvivesModelReconcile(t *testing.T) {
	before := []byte(`{"model":"claude-opus-5-5","system":[{"type":"text","text":"billing"}]}`)
	cloaked := []byte(`{"model":"claude-opus-5-5","fallbacks":[{"model":"claude-opus-4-8"}],"system":[{"type":"text","text":"billing"}]}`)
	state := captureClaudeCodeFableState(before, cloaked, true)
	for _, tt := range []struct {
		name, body, wantFallback string
	}{
		{name: "Fable 5.2 keeps its injected fallback", body: `{"model":"claude-fable-5-2","fallbacks":[{"model":"claude-opus-5"}],"system":[]}`, wantFallback: "claude-opus-5"},
		{name: "Opus fallback dropped on rewrite to Fable 5.2", body: `{"model":"claude-fable-5-2","fallbacks":[{"model":"claude-opus-4-8"}],"system":[]}`},
		{name: "Opus fallback dropped on rewrite to Mythos 5.2", body: `{"model":"claude-mythos-5-2","fallbacks":[{"model":"claude-opus-4-8"}],"system":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := reconcileClaudeCodeFableModelAfterPayload([]byte(tt.body), state, false, false, true, false)
			if model := ojson.GetBytes(got, "fallbacks.0.model").String(); model != tt.wantFallback {
				t.Errorf("fallback = %q, want %q", model, tt.wantFallback)
			}
		})
	}
}
