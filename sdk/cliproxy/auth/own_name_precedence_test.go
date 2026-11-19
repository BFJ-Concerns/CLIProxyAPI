package auth

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// A name another provider serves as its own is never handed to the
// Antigravity credential that folds its effort-named copy into the same name,
// whether that provider's credential is available or benched; the copy stays
// reachable under its effort-named id, and the fold takes the name once the
// other provider's credential is gone.
func TestPickKeepsAnOwnNameWithTheProviderThatServesIt(t *testing.T) {
	const (
		model   = "own-name-precedence-model"
		variant = model + "-high"
	)
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("own-name-claude", "claude", []*registry.ModelInfo{{ID: model, OwnedBy: "anthropic"}})
	reg.RegisterClient("own-name-antigravity", "antigravity", registry.FoldEffortVariants([]*registry.ModelInfo{
		{ID: variant, OwnedBy: "antigravity", Thinking: &registry.ThinkingSupport{Levels: []string{"high"}}},
	}))
	t.Cleanup(func() {
		reg.UnregisterClient("own-name-claude")
		reg.UnregisterClient("own-name-antigravity")
	})

	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(&customStreamMockExecutor{identifier: "claude"})
	manager.RegisterExecutor(&customStreamMockExecutor{identifier: "antigravity"})
	for _, candidate := range []*Auth{
		{ID: "own-name-claude", Provider: "claude", Status: StatusActive},
		{ID: "own-name-antigravity", Provider: "antigravity", Status: StatusActive},
	} {
		if _, errRegister := manager.Register(context.Background(), candidate); errRegister != nil {
			t.Fatal(errRegister)
		}
	}
	both := []string{"claude", "antigravity"}
	pick := func(requested string) (string, error) {
		selected, _, _, errPick := manager.pickNextMixed(context.Background(), both, requested, cliproxyexecutor.Options{}, nil)
		if errPick != nil {
			return "", errPick
		}
		return selected.ID, nil
	}

	if providers := reg.GetModelProviders(model); len(providers) != 1 || providers[0] != "claude" {
		t.Fatalf("providers for %s = %v, want only claude", model, providers)
	}
	for i := 0; i < 4; i++ {
		if picked, errPick := pick(model); errPick != nil || picked != "own-name-claude" {
			t.Fatalf("pick %d for %s = %q, %v; want the Claude credential", i, model, picked, errPick)
		}
	}
	if picked, errPick := pick(variant); errPick != nil || picked != "own-name-antigravity" {
		t.Fatalf("pick for %s = %q, %v; want the Antigravity credential", variant, picked, errPick)
	}

	claude, _ := manager.GetByID("own-name-claude")
	claude.ModelStates = map[string]*ModelState{model: {
		Status: StatusError, Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour),
	}}
	if _, errUpdate := manager.Update(context.Background(), claude); errUpdate != nil {
		t.Fatal(errUpdate)
	}
	if picked, errPick := pick(model); errPick == nil {
		t.Fatalf("pick for %s with the Claude credential benched = %q; want no credential", model, picked)
	}

	reg.UnregisterClient("own-name-claude")
	if picked, errPick := pick(model); errPick != nil || picked != "own-name-antigravity" {
		t.Fatalf("pick for %s with no Claude credential = %q, %v; want the Antigravity fold", model, picked, errPick)
	}
}
