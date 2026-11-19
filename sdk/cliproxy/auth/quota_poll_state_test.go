package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestQuotaPollerRestoresColdSessionRouting(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	first, transport, exhausted := newCountedQuotaPoller(t, http.StatusOK,
		`{"limits":[{"kind":"weekly_all","percent":100}]}`)
	first.manager.SetConfig(&internalconfig.Config{AuthDir: dir})
	first.statePath = quotaPollStatePath(first.manager)

	available := subscriptionAuth("acct-b")
	available.Metadata = map[string]any{"access_token": "test-token"}
	first.schedule[exhausted.ID] = &quotaPollSchedule{inFlight: true}
	first.poll(exhausted, now)
	transport.body = `{"limits":[{"kind":"weekly_all","percent":20}]}`
	first.schedule[available.ID] = &quotaPollSchedule{inFlight: true}
	first.poll(available, now)
	first.Stop()

	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{Fallback: newTestQuotaSelector()})
	t.Cleanup(selector.Stop)
	manager := NewManager(nil, selector, nil)
	manager.SetConfig(&internalconfig.Config{AuthDir: dir})
	candidates := []*Auth{subscriptionAuth(exhausted.ID), subscriptionAuth(available.ID)}
	for _, candidate := range candidates {
		if _, err := manager.Register(context.Background(), candidate); err != nil {
			t.Fatalf("register auth %s: %v", candidate.ID, err)
		}
	}

	BindQuotaPoller(manager, manager.Selector())
	t.Cleanup(func() { BindQuotaPoller(manager, nil) })
	opts := cliproxyexecutor.Options{Headers: http.Header{"X-Session-Id": []string{"fable-session"}}}
	chosen, err := manager.Selector().Pick(context.Background(), "claude", "claude-fable-5", opts, candidates)
	if err != nil {
		t.Fatalf("cold session pick: %v", err)
	}
	if chosen == nil || chosen.ID != available.ID {
		t.Fatalf("cold session chose %v, want %s based on restored readings", chosen, available.ID)
	}
}

func TestQuotaPollerRestoresFloorForInterruptedPoll(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	first, _, auth := newCountedQuotaPoller(t, http.StatusOK, "")
	first.manager.SetConfig(&internalconfig.Config{AuthDir: t.TempDir()})
	first.statePath = quotaPollStatePath(first.manager)
	first.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}
	if !first.begin(auth.ID, now) {
		t.Fatal("initial poll did not start")
	}
	first.Stop()

	restarted, transport, _ := newCountedQuotaPoller(t, http.StatusOK, "{}")
	restarted.statePath = first.statePath
	restarted.restoreState()
	restarted.sweep(now.Add(time.Second))
	if quotaPollerInFlight(restarted, auth.ID) || transport.count() != 0 {
		t.Fatal("restart launched a poll inside the interrupted poll's cadence floor")
	}
	restarted.sweep(now.Add(quotaPollFloor))
	waitForQuotaPollerRequest(t, transport.issued)
}
