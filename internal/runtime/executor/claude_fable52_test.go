package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

func TestClaudeExecutor_Fable52PreservedThinkingReplayInjectsDropBlock(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		t.Run(model, func(t *testing.T) {
			seenBody, seenHeaders := runClaudeExecutorCapture(t, nil, "", false, model, preservedThinkingReplayPayload(model))
			if got := gjson.GetBytes(seenBody, "thinking.block_binding.prefix_mismatch_behavior").String(); got != "drop_block" {
				t.Fatalf("thinking.block_binding.prefix_mismatch_behavior = %q, want drop_block; body=%s", got, seenBody)
			}
			if betas := seenHeaders.Get("Anthropic-Beta"); !strings.Contains(betas, claudeThinkingBindingBeta) {
				t.Fatalf("Anthropic-Beta = %q, want %s", betas, claudeThinkingBindingBeta)
			}
		})
	}
}

func TestApplyCloaking_Fable52Additions(t *testing.T) {
	for _, tt := range []struct {
		model        string
		wantFallback string
	}{
		{model: "claude-fable-5-2", wantFallback: "claude-opus-5"},
		{model: "claude-mythos-5-2"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			const apiKey = "sk-ant-oat-fable52-test"
			cfg := &config.Config{ClaudeKey: []config.ClaudeKey{{APIKey: apiKey, Cloak: &config.CloakConfig{}}}}
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": apiKey}}
			payload := []byte(`{"model":"` + tt.model + `","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"test"}]}`)

			out, cloaked, err := applyCloaking(context.Background(), cfg, auth, payload, apiKey, false, true)
			if err != nil {
				t.Fatalf("applyCloaking() error = %v", err)
			}
			if !cloaked {
				t.Fatal("applyCloaking() cloaked = false, want true")
			}

			if got := gjson.GetBytes(out, "fallbacks.0.model").String(); got != tt.wantFallback {
				t.Fatalf("fallback model = %q, want %q; fallbacks=%s", got, tt.wantFallback, gjson.GetBytes(out, "fallbacks").Raw)
			}
			if got := gjson.GetBytes(out, "thinking.display").String(); got != "updates" {
				t.Fatalf("thinking.display = %q, want updates", got)
			}
			for i, block := range gjson.GetBytes(out, "system").Array() {
				if strings.Contains(block.Get("text").String(), "Reporting outcomes") {
					t.Fatalf("system block %d carries the Fable 5.1 Reporting outcomes block", i)
				}
			}

			betas := claudeCodeCLIBetas(out, nil, true)
			if !strings.Contains(betas, claudePerTurnControlBeta) {
				t.Fatalf("betas = %q, want %s", betas, claudePerTurnControlBeta)
			}
			if gotFallbackBeta := strings.Contains(betas, claudeServerSideFallbackBeta); gotFallbackBeta != (tt.wantFallback != "") {
				t.Fatalf("server-side fallback beta present = %t, want %t; betas=%s", gotFallbackBeta, tt.wantFallback != "", betas)
			}
		})
	}
}

func TestClaudeFable52FallbackReconcilesAfterModelOverride(t *testing.T) {
	before := []byte(`{"model":"claude-opus-5-5","system":[{"type":"text","text":"billing"}]}`)
	cloaked := []byte(`{"model":"claude-opus-5-5","fallbacks":[{"model":"claude-opus-4-8"}],"system":[{"type":"text","text":"billing"}]}`)
	state := captureClaudeCodeFableState(before, cloaked, true)
	for _, tt := range []struct {
		name, body, wantFallback string
	}{
		{name: "Opus to Fable 5.2", body: `{"model":"claude-fable-5-2","fallbacks":[{"model":"claude-opus-4-8"}],"system":[]}`, wantFallback: "claude-opus-5"},
		{name: "Opus to Mythos 5.2", body: `{"model":"claude-mythos-5-2","fallbacks":[{"model":"claude-opus-4-8"}],"system":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := reconcileClaudeCodeFableModelAfterPayload([]byte(tt.body), state, false, false, true, false)
			if model := gjson.GetBytes(got, "fallbacks.0.model").String(); model != tt.wantFallback {
				t.Errorf("fallback = %q, want %q", model, tt.wantFallback)
			}
		})
	}

	got := reconcileClaudeCodeFableModelAfterPayload(
		[]byte(`{"model":"claude-fable-5-2","system":[]}`), claudeCodeFableState{}, false, false, true, false,
	)
	if fallback := gjson.GetBytes(got, "fallbacks.0.model").String(); fallback != "claude-opus-5" {
		t.Errorf("Sonnet rewritten to Fable 5.2 fallback = %q, want claude-opus-5", fallback)
	}
}

func TestClaudeFable52ReconcileDropsInjectedReportingBlock(t *testing.T) {
	before := []byte(`{"model":"claude-fable-5-1","system":[]}`)
	cloaked := []byte(`{"model":"claude-fable-5-1","system":[` + buildTextBlock(claudeCodeFableReportingOutcomes, nil) + `]}`)
	state := captureClaudeCodeFableState(before, cloaked, true)

	rewritten := []byte(`{"model":"claude-fable-5-2","system":[` + buildTextBlock(claudeCodeFableReportingOutcomes, nil) + `]}`)
	got := reconcileClaudeCodeFableModelAfterPayload(rewritten, state, false, false, true, false)
	if hasFableReportingBlock(got) {
		t.Fatalf("Fable 5.1 rewritten to 5.2 kept the 5.1 Reporting outcomes block: %s", got)
	}
}
