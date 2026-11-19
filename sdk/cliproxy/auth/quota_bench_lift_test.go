package auth

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

const (
	benchLiftOpusModel  = "claude-opus-bench-lift"
	benchLiftFableModel = "claude-fable-bench-lift"
)

// newBenchLiftManager registers one Claude subscription credential serving
// a Fable and a non-Fable model, with a file cooldown store.
func newBenchLiftManager(t *testing.T, authID string) (*Manager, string) {
	t.Helper()
	previous := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(previous) })

	storeDir := t.TempDir()
	manager := NewManager(nil, nil, nil)
	manager.SetCooldownStateStore(NewFileCooldownStateStore(storeDir))
	registry.GetGlobalRegistry().RegisterClient(authID, "claude", []*registry.ModelInfo{{ID: benchLiftOpusModel}, {ID: benchLiftFableModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(authID) })
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), subscriptionAuth(authID)); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	return manager, storeDir
}

// benchClaude records a 429 carrying the given Anthropic rejection headers.
func benchClaude(manager *Manager, authID, model string, credentialScope bool, retryAfter time.Duration, headers http.Header) {
	markClaudeCooldown(manager, authID, model, credentialScope, retryAfter, headers, &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"})
}

func markClaudeCooldown(manager *Manager, authID, model string, credentialScope bool, retryAfter time.Duration, headers http.Header, resultErr *Error) {
	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	internallogging.SetResponseHeaders(ctx, headers)
	manager.MarkResult(ctx, Result{
		AuthID:          authID,
		Provider:        "claude",
		Model:           model,
		Success:         false,
		CredentialScope: credentialScope,
		RetryAfter:      &retryAfter,
		Error:           resultErr,
	})
}

func weeklyRejectionHeaders(reset time.Time) http.Header {
	return http.Header{
		"Anthropic-Ratelimit-Unified-Status":               {"rejected"},
		"Anthropic-Ratelimit-Unified-5h-Status":            {"allowed"},
		"Anthropic-Ratelimit-Unified-7d-Status":            {"rejected"},
		"Anthropic-Ratelimit-Unified-7d-Utilization":       {"1.0"},
		"Anthropic-Ratelimit-Unified-7d-Reset":             {strconv.FormatInt(reset.Unix(), 10)},
		"Anthropic-Ratelimit-Unified-5h-Utilization":       {"0.2"},
		"Anthropic-Ratelimit-Unified-Representative-Claim": {"seven_day"},
	}
}

func fableRejectionHeaders(reset time.Time) http.Header {
	return http.Header{
		"Anthropic-Ratelimit-Unified-Status":                  {"rejected"},
		"Anthropic-Ratelimit-Unified-5h-Status":               {"allowed"},
		"Anthropic-Ratelimit-Unified-7d-Status":               {"allowed"},
		"Anthropic-Ratelimit-Unified-7d_oi-Status":            {"rejected"},
		"Anthropic-Ratelimit-Unified-7d_oi-Utilization":       {"1.0"},
		"Anthropic-Ratelimit-Unified-7d_oi-Reset":             {strconv.FormatInt(reset.Unix(), 10)},
		"Anthropic-Ratelimit-Unified-Overage-Status":          {"rejected"},
		"Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": {"out_of_credits"},
	}
}

func benchLiftAvailable(t *testing.T, manager *Manager, authID, model string) bool {
	t.Helper()
	auth, ok := manager.GetByID(authID)
	if !ok || auth == nil {
		t.Fatalf("auth %s missing", authID)
	}
	return IsAuthAvailableForModel(auth, model, time.Now())
}

func persistedLiveBench(t *testing.T, storeDir, authID, model string) bool {
	t.Helper()
	records, errLoad := NewFileCooldownStateStore(storeDir).Load(context.Background())
	if errLoad != nil {
		t.Fatalf("load cooldown store: %v", errLoad)
	}
	for _, record := range records {
		if record.AuthID == authID && record.Model == model && record.NextRetryAfter.After(time.Now()) {
			return true
		}
	}
	return false
}

func TestLiftWindowBenches_CredentialBenchLiftsWhenWeeklyReadingFalls(t *testing.T) {
	authID := "bench-lift-credential"
	manager, storeDir := newBenchLiftManager(t, authID)
	weeklyReset := time.Now().Add(3 * 24 * time.Hour)
	benchClaude(manager, authID, benchLiftOpusModel, true, time.Until(weeklyReset), weeklyRejectionHeaders(weeklyReset))
	manager.PersistCooldownStates(context.Background())

	if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) || benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("credential-wide bench did not withhold the credential")
	}
	if !persistedLiveBench(t, storeDir, authID, "") && !persistedLiveBench(t, storeDir, authID, benchLiftOpusModel) {
		t.Fatal("credential-wide bench was not persisted")
	}

	// The weekly window still reads full: the bench stands.
	manager.liftWindowBenches(authID, QuotaUsage{
		Session: &QuotaWindow{Percent: 20, ResetsAt: time.Now().Add(2 * time.Hour)},
		Weekly:  &QuotaWindow{Percent: 100, ResetsAt: weeklyReset},
	}, time.Now(), time.Now())
	if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
		t.Fatal("bench lifted while the weekly window still read full")
	}

	// The weekly window reset early: same reset time reported, reading fell.
	manager.liftWindowBenches(authID, QuotaUsage{
		Session: &QuotaWindow{Percent: 20, ResetsAt: time.Now().Add(2 * time.Hour)},
		Weekly:  &QuotaWindow{Percent: 4, ResetsAt: weeklyReset},
	}, time.Now(), time.Now())
	if !benchLiftAvailable(t, manager, authID, benchLiftOpusModel) || !benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("credential-wide bench survived a poll showing weekly headroom")
	}
	for _, model := range []string{"", benchLiftOpusModel, benchLiftFableModel} {
		if persistedLiveBench(t, storeDir, authID, model) {
			t.Fatalf("persisted bench for model %q survived the lift", model)
		}
	}
}

func TestLiftWindowBenches_ModelBenchLiftsWhenFableReadingFalls(t *testing.T) {
	authID := "bench-lift-model"
	manager, storeDir := newBenchLiftManager(t, authID)
	fableReset := time.Now().Add(4 * 24 * time.Hour)
	benchClaude(manager, authID, benchLiftFableModel, false, time.Until(fableReset), fableRejectionHeaders(fableReset))
	if !persistedLiveBench(t, storeDir, authID, benchLiftFableModel) {
		t.Fatal("Fable bench was not persisted before the lift")
	}

	if benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("model-scoped bench did not withhold the Fable model")
	}
	if !benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
		t.Fatal("model-scoped bench withheld an unrelated model")
	}

	manager.liftWindowBenches(authID, QuotaUsage{
		Weekly: &QuotaWindow{Percent: 30, ResetsAt: fableReset},
		Fable:  &QuotaWindow{Percent: 100, ResetsAt: fableReset},
	}, time.Now(), time.Now())
	if benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("Fable bench lifted while the Fable window still read full")
	}

	manager.liftWindowBenches(authID, QuotaUsage{
		Weekly: &QuotaWindow{Percent: 30, ResetsAt: fableReset},
		Fable:  &QuotaWindow{Percent: 0, ResetsAt: time.Now().Add(7 * 24 * time.Hour)},
	}, time.Now(), time.Now())
	if !benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("Fable bench survived a poll showing Fable headroom")
	}
	records, errLoad := NewFileCooldownStateStore(storeDir).Load(context.Background())
	if errLoad != nil {
		t.Fatalf("load cooldown store after lift: %v", errLoad)
	}
	for _, record := range records {
		if record.AuthID == authID && record.Model == benchLiftFableModel {
			t.Fatalf("Fable bench survived in the cooldown store: %+v", record)
		}
	}
}

func TestLiftWindowBenches_CredentialLiftKeepsModelBenchOnFullWindow(t *testing.T) {
	authID := "bench-lift-mixed"
	manager, _ := newBenchLiftManager(t, authID)
	fableReset := time.Now().Add(4 * 24 * time.Hour)
	weeklyReset := time.Now().Add(2 * 24 * time.Hour)
	benchClaude(manager, authID, benchLiftFableModel, false, time.Until(fableReset), fableRejectionHeaders(fableReset))
	benchClaude(manager, authID, benchLiftOpusModel, true, time.Until(weeklyReset), weeklyRejectionHeaders(weeklyReset))

	manager.liftWindowBenches(authID, QuotaUsage{
		Weekly: &QuotaWindow{Percent: 10, ResetsAt: weeklyReset},
		Fable:  &QuotaWindow{Percent: 100, ResetsAt: fableReset},
	}, time.Now(), time.Now())
	if !benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
		t.Fatal("credential-wide bench survived a poll showing weekly headroom")
	}
	if benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("lifting the credential bench also freed the Fable model whose window still reads full")
	}
}

func TestLiftWindowBenches_BenchWithoutWindowSignalsStands(t *testing.T) {
	authID := "bench-lift-unknown-window"
	manager, _ := newBenchLiftManager(t, authID)
	benchClaude(manager, authID, benchLiftOpusModel, true, time.Hour, http.Header{"Retry-After": {"3600"}})

	manager.liftWindowBenches(authID, QuotaUsage{
		Session: &QuotaWindow{Percent: 0},
		Weekly:  &QuotaWindow{Percent: 0},
		Fable:   &QuotaWindow{Percent: 0},
	}, time.Now(), time.Now())
	if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
		t.Fatal("a bench recorded without window signals was lifted")
	}
}

func TestQuotaPoll_SuccessfulPollLiftsBench(t *testing.T) {
	poller, transport, auth := newCountedQuotaPoller(t, http.StatusOK, `{"limits":[{"kind":"session","percent":5},{"kind":"weekly_all","percent":3}]}`)
	manager := poller.manager
	registry.GetGlobalRegistry().RegisterClient(auth.ID, "claude", []*registry.ModelInfo{{ID: benchLiftOpusModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	weeklyReset := time.Now().Add(3 * 24 * time.Hour)
	benchClaude(manager, auth.ID, benchLiftOpusModel, true, time.Until(weeklyReset), weeklyRejectionHeaders(weeklyReset))
	if benchLiftAvailable(t, manager, auth.ID, benchLiftOpusModel) {
		t.Fatal("bench did not withhold the credential")
	}

	current, _ := manager.GetByID(auth.ID)
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}
	poller.poll(current, time.Now())
	if transport.count() != 1 {
		t.Fatalf("poll requests = %d, want 1", transport.count())
	}
	if !benchLiftAvailable(t, manager, auth.ID, benchLiftOpusModel) {
		t.Fatal("successful poll showing weekly headroom did not lift the bench")
	}
}

func TestQuotaPoll_BenchRecordedDuringRequestStands(t *testing.T) {
	for _, tc := range []struct {
		name            string
		credentialScope bool
	}{
		{name: "model"},
		{name: "credential", credentialScope: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authID := "bench-lift-in-flight-" + tc.name
			manager, _ := newBenchLiftManager(t, authID)
			current, _ := manager.GetByID(authID)
			current.Metadata = map[string]any{"access_token": "test-token"}
			poller := newQuotaUsagePoller(manager, NewQuotaUsageStore())
			t.Cleanup(poller.Stop)
			poller.schedule[authID] = &quotaPollSchedule{inFlight: true}
			now := time.Now()
			reset := now.Add(24 * time.Hour)
			transport := &quotaPollerCountedTransport{
				issued: make(chan struct{}, 2),
				status: http.StatusOK,
				body:   `{"limits":[{"kind":"weekly_all","percent":40}]}`,
				onRequest: func() {
					benchClaude(manager, authID, benchLiftOpusModel, tc.credentialScope, time.Until(reset), weeklyRejectionHeaders(reset))
					if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
						t.Fatal("429 did not bench the requested model")
					}
					if got := benchLiftAvailable(t, manager, authID, benchLiftFableModel); got == tc.credentialScope {
						t.Fatalf("unrelated model available = %t for credential scope %t", got, tc.credentialScope)
					}
				},
			}
			poller.clients[""] = &http.Client{Transport: transport}

			poller.poll(current, now.Add(-time.Second))
			if _, ok := poller.store.Get(authID); !ok {
				t.Fatal("in-flight poll did not publish its successful reading")
			}
			if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
				t.Error("poll lifted a bench recorded during its request")
			}

			transport.onRequest = nil
			poller.schedule[authID].inFlight = true
			poller.poll(current, time.Now())
			if !benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
				t.Fatal("poll started after the rejection did not lift the bench")
			}
		})
	}
}

func TestLiftWindowBenches_UsesSessionRejectionLevel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		percent  float64
		wantLift bool
	}{
		{name: "below", percent: 74, wantLift: true},
		{name: "equal", percent: 75},
		{name: "above", percent: 76},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authID := "bench-lift-session-" + tc.name
			manager, _ := newBenchLiftManager(t, authID)
			reset := time.Now().Add(2 * time.Hour)
			headers := http.Header{
				"Anthropic-Ratelimit-Unified-Status":               {"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Status":            {"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Utilization":       {"0.75"},
				"Anthropic-Ratelimit-Unified-5h-Reset":             {strconv.FormatInt(reset.Unix(), 10)},
				"Anthropic-Ratelimit-Unified-7d-Status":            {"allowed"},
				"Anthropic-Ratelimit-Unified-Representative-Claim": {"five_hour"},
			}
			benchClaude(manager, authID, benchLiftOpusModel, false, time.Until(reset), headers)
			if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
				t.Fatal("session rejection did not bench the model")
			}

			now := time.Now()
			manager.liftWindowBenches(authID, QuotaUsage{
				Session: &QuotaWindow{Percent: tc.percent, ResetsAt: reset},
				Weekly:  &QuotaWindow{Percent: 10, ResetsAt: reset},
			}, now, now)
			if got := benchLiftAvailable(t, manager, authID, benchLiftOpusModel); got != tc.wantLift {
				t.Fatalf("model available = %t at %.0f%% after rejection at 75%%, want %t", got, tc.percent, tc.wantLift)
			}
		})
	}
}

func TestLiftWindowBenches_PreservesForceAndNonQuotaCooldowns(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *Error
	}{
		{name: "force", err: &Error{HTTPStatus: http.StatusTooManyRequests, Code: ErrorCodeForceCooldown}},
		{name: "non-quota", err: &Error{HTTPStatus: http.StatusServiceUnavailable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authID := "bench-lift-protected-" + tc.name
			manager, _ := newBenchLiftManager(t, authID)
			reset := time.Now().Add(time.Hour)
			headers := weeklyRejectionHeaders(reset)
			// An overlapping 503 must survive even when the earlier quota rejection can be lifted.
			if tc.err.HTTPStatus == http.StatusServiceUnavailable {
				benchClaude(manager, authID, benchLiftOpusModel, false, time.Until(reset), headers)
				headers = nil
			}
			markClaudeCooldown(manager, authID, benchLiftOpusModel, false, time.Until(reset), headers, tc.err)
			if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
				t.Fatal("cooldown did not withhold the model")
			}

			now := time.Now()
			manager.liftWindowBenches(authID, QuotaUsage{
				Weekly: &QuotaWindow{Percent: 10, ResetsAt: reset},
			}, now, now)
			if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) {
				t.Fatal("poll showing headroom cleared a protected cooldown")
			}
		})
	}
}
