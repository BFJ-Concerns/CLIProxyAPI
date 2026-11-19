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
	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	internallogging.SetResponseHeaders(ctx, headers)
	manager.MarkResult(ctx, Result{
		AuthID:          authID,
		Provider:        "claude",
		Model:           model,
		Success:         false,
		CredentialScope: credentialScope,
		RetryAfter:      &retryAfter,
		Error:           &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"},
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
	manager, _ := newBenchLiftManager(t, authID)
	fableReset := time.Now().Add(4 * 24 * time.Hour)
	benchClaude(manager, authID, benchLiftFableModel, false, time.Until(fableReset), fableRejectionHeaders(fableReset))

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
	poller.poll(current, time.Now())
	if transport.count() != 1 {
		t.Fatalf("poll requests = %d, want 1", transport.count())
	}
	if !benchLiftAvailable(t, manager, auth.ID, benchLiftOpusModel) {
		t.Fatal("successful poll showing weekly headroom did not lift the bench")
	}
}

func TestLiftWindowBenches_BenchRecordedAfterPollStartStands(t *testing.T) {
	authID := "bench-lift-stale-poll"
	manager, _ := newBenchLiftManager(t, authID)
	weeklyReset := time.Now().Add(3 * 24 * time.Hour)
	fableReset := time.Now().Add(4 * 24 * time.Hour)

	// The poll starts and reads headroom; the 429s that exhaust the
	// windows are recorded before its reading arrives.
	polledAt := time.Now().Add(-time.Second)
	benchClaude(manager, authID, benchLiftFableModel, false, time.Until(fableReset), fableRejectionHeaders(fableReset))
	benchClaude(manager, authID, benchLiftOpusModel, true, time.Until(weeklyReset), weeklyRejectionHeaders(weeklyReset))

	staleReading := QuotaUsage{
		Weekly: &QuotaWindow{Percent: 40, ResetsAt: weeklyReset},
		Fable:  &QuotaWindow{Percent: 40, ResetsAt: fableReset},
	}
	manager.liftWindowBenches(authID, staleReading, polledAt, time.Now())
	if benchLiftAvailable(t, manager, authID, benchLiftOpusModel) || benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("a poll that started before the benches were recorded lifted them")
	}

	// A poll started after the benches judges them.
	manager.liftWindowBenches(authID, staleReading, time.Now(), time.Now())
	if !benchLiftAvailable(t, manager, authID, benchLiftOpusModel) || !benchLiftAvailable(t, manager, authID, benchLiftFableModel) {
		t.Fatal("a poll started after the benches did not lift them")
	}
}
