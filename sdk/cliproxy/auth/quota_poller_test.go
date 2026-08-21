package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type quotaPollerCountedTransport struct {
	mu       sync.Mutex
	requests int
	issued   chan struct{}
	status   int
	header   http.Header
	body     string
}

func (t *quotaPollerCountedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.requests++
	t.mu.Unlock()
	t.issued <- struct{}{}
	return &http.Response{
		StatusCode: t.status,
		Header:     t.header.Clone(),
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func (t *quotaPollerCountedTransport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.requests
}

func quotaPollerInFlight(poller *quotaUsagePoller, authID string) bool {
	poller.mu.Lock()
	defer poller.mu.Unlock()
	return poller.schedule[authID].inFlight
}

func waitForQuotaPollerRequest(t *testing.T, issued <-chan struct{}) {
	t.Helper()
	select {
	case <-issued:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for quota poll request")
	}
}

func newCountedQuotaPoller(t *testing.T, status int, body string) (*quotaUsagePoller, *quotaPollerCountedTransport, *Auth) {
	t.Helper()
	manager := NewManager(nil, nil, nil)
	auth := subscriptionAuth("acct-a")
	auth.Metadata = map[string]any{"access_token": "test-token"}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	transport := &quotaPollerCountedTransport{
		issued: make(chan struct{}, 4),
		status: status,
		body:   body,
	}
	poller := newQuotaUsagePoller(manager, NewQuotaUsageStore())
	poller.clients[""] = &http.Client{Transport: transport}
	t.Cleanup(poller.Stop)
	return poller, transport, auth
}

func TestParseClaudeUsageResponseUnifiedLimits(t *testing.T) {
	body := []byte(`{
		"limits": [
			{"kind": "session", "percent": 12.5, "resets_at": "2026-08-21T15:00:00Z"},
			{"kind": "weekly_all", "percent": 40, "resets_at": "2026-08-24T09:00:00Z"},
			{"kind": "weekly_scoped", "percent": 5, "resets_at": "2026-08-25T09:00:00Z"},
			{"kind": "weekly_scoped", "percent": 15, "resets_at": "2026-08-25T09:00:00Z"}
		]
	}`)
	polledAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	usage, err := parseClaudeUsageResponse(body, polledAt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if usage.Session == nil || usage.Session.Percent != 12.5 {
		t.Fatalf("session = %+v, want 12.5%%", usage.Session)
	}
	if usage.Weekly == nil || usage.Weekly.Percent != 40 {
		t.Fatalf("weekly = %+v, want 40%%", usage.Weekly)
	}
	// Duplicate weekly_scoped entries: the fullest wins.
	if usage.Fable == nil || usage.Fable.Percent != 15 {
		t.Fatalf("fable = %+v, want 15%%", usage.Fable)
	}
	wantReset := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	if !usage.Weekly.ResetsAt.Equal(wantReset) {
		t.Fatalf("weekly reset = %v, want %v", usage.Weekly.ResetsAt, wantReset)
	}
	if !usage.UpdatedAt.Equal(polledAt) {
		t.Fatalf("UpdatedAt = %v, want %v", usage.UpdatedAt, polledAt)
	}
}

func TestParseClaudeUsageResponseLegacyFallback(t *testing.T) {
	body := []byte(`{
		"five_hour": {"utilization": 30, "resets_at": "2026-08-21T15:00:00Z"},
		"seven_day": {"utilization": 55, "resets_at": "2026-08-27T00:00:00Z"}
	}`)
	usage, err := parseClaudeUsageResponse(body, time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if usage.Session == nil || usage.Session.Percent != 30 {
		t.Fatalf("session = %+v, want 30%%", usage.Session)
	}
	if usage.Weekly == nil || usage.Weekly.Percent != 55 {
		t.Fatalf("weekly = %+v, want 55%%", usage.Weekly)
	}
	if usage.Fable != nil {
		t.Fatalf("fable = %+v, want nil (not reported)", usage.Fable)
	}
}

func TestParseClaudeUsageResponseEmptyIsError(t *testing.T) {
	if _, err := parseClaudeUsageResponse([]byte(`{}`), time.Now()); err == nil {
		t.Fatal("expected error for a response with no limits")
	}
	if _, err := parseClaudeUsageResponse([]byte(`not json`), time.Now()); err == nil {
		t.Fatal("expected error for a malformed response")
	}
}

func TestQuotaRetryAtHonoursRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		header string
		want   time.Time
	}{
		{"missing header falls back to five minutes", "", now.Add(quotaThrottleFallback)},
		{"seconds value honoured", "120", now.Add(120 * time.Second)},
		{"http date honoured", now.Add(10 * time.Minute).UTC().Format(http.TimeFormat), now.Add(10 * time.Minute)},
		{"past value falls back", "-5", now.Add(quotaThrottleFallback)},
		{"long suppression honoured in full", "86400", now.Add(24 * time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.header != "" {
				header.Set("Retry-After", tc.header)
			}
			got := quotaRetryAt(header, now)
			// http.TimeFormat has second precision.
			if diff := got.Sub(tc.want); diff > time.Second || diff < -time.Second {
				t.Fatalf("quotaRetryAt = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQuotaPollIntervalTightensTowardTheFloor(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	cases := []struct {
		name string
		peak float64
		want time.Duration
	}{
		{"comfortable", 10, 30 * time.Minute},
		{"warming", 60, 15 * time.Minute},
		{"tight", 80, 5 * time.Minute},
		{"near exhaustion", 95, 2 * time.Minute},
		{"critical hits the floor", 99, quotaPollFloor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := QuotaUsage{
				Session: window(5, future),
				Weekly:  window(tc.peak, future),
				Fable:   window(5, future),
			}
			if got := quotaPollInterval(usage, now); got != tc.want {
				t.Fatalf("interval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQuotaPollerFinishEnforcesCadenceFloor(t *testing.T) {
	p := newQuotaUsagePoller(nil, NewQuotaUsageStore())
	defer p.Stop()
	started := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	p.schedule["acct-a"] = &quotaPollSchedule{inFlight: true}

	// A success wanting an immediate re-poll is still held to the floor.
	p.finish("acct-a", started, started.Add(time.Second), false)
	sched := p.schedule["acct-a"]
	if sched.inFlight {
		t.Fatal("finish left the schedule in flight")
	}
	if got, want := sched.nextPollAt, started.Add(quotaPollFloor); !got.Equal(want) {
		t.Fatalf("nextPollAt = %v, want floor %v", got, want)
	}

	// A later slot is untouched by the floor.
	p.schedule["acct-a"].inFlight = true
	p.finish("acct-a", started, started.Add(30*time.Minute), false)
	if got, want := p.schedule["acct-a"].nextPollAt, started.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("nextPollAt = %v, want %v", got, want)
	}
}

func TestQuotaPollerSweepHonoursRetryAfterSuppression(t *testing.T) {
	poller, transport, auth := newCountedQuotaPoller(t, http.StatusTooManyRequests, "")
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(10 * time.Minute)
	transport.header = http.Header{"Retry-After": []string{"600"}}
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}

	// Drive a real 429 through poll so the product derives and schedules the
	// Retry-After deadline that the subsequent sweeps must honour.
	poller.poll(auth, now)
	waitForQuotaPollerRequest(t, transport.issued)
	if got := transport.count(); got != 1 {
		t.Fatalf("requests after throttled poll = %d, want 1", got)
	}

	poller.sweep(now.Add(2 * time.Minute))
	if quotaPollerInFlight(poller, auth.ID) {
		t.Fatal("poll launched before Retry-After expiry")
	}
	if got := transport.count(); got != 1 {
		t.Fatalf("requests before Retry-After expiry = %d, want 1", got)
	}

	poller.sweep(retryAt)
	waitForQuotaPollerRequest(t, transport.issued)
	if got := transport.count(); got != 2 {
		t.Fatalf("requests at Retry-After expiry = %d, want 2", got)
	}
}

func TestQuotaPollerSweepHonoursCadenceFloor(t *testing.T) {
	body := `{"limits":[{"kind":"weekly_all","percent":99,"resets_at":"2026-08-22T12:00:00Z"}]}`
	poller, transport, auth := newCountedQuotaPoller(t, http.StatusOK, body)
	polledAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nextAllowed := polledAt.Add(quotaPollFloor)
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}

	// A completed critical-usage poll asks for the shortest adaptive cadence;
	// the scheduler must still suppress another request inside the floor.
	poller.poll(auth, polledAt)
	waitForQuotaPollerRequest(t, transport.issued)
	if got := transport.count(); got != 1 {
		t.Fatalf("requests after completed poll = %d, want 1", got)
	}

	poller.sweep(polledAt.Add(10 * time.Second))
	if quotaPollerInFlight(poller, auth.ID) {
		t.Fatal("poll launched inside cadence floor")
	}
	if got := transport.count(); got != 1 {
		t.Fatalf("requests inside cadence floor = %d, want 1", got)
	}

	poller.sweep(nextAllowed)
	waitForQuotaPollerRequest(t, transport.issued)
	if got := transport.count(); got != 2 {
		t.Fatalf("requests at cadence floor = %d, want 2", got)
	}
}

func TestQuotaPollerTransientFailureRetainsStaleState(t *testing.T) {
	poller, transport, auth := newCountedQuotaPoller(t, http.StatusInternalServerError, "")
	polledAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	stale := QuotaUsage{
		Session:   window(25, polledAt.Add(time.Hour)),
		Weekly:    window(40, polledAt.Add(24*time.Hour)),
		Fable:     window(15, polledAt.Add(48*time.Hour)),
		UpdatedAt: polledAt.Add(-30 * time.Minute),
	}
	neverPolled := subscriptionAuth("never-polled")
	neverPolled.Disabled = true
	neverPolled.Metadata = map[string]any{"access_token": "unused-test-token"}
	if _, err := poller.manager.Register(context.Background(), neverPolled); err != nil {
		t.Fatalf("register never-polled auth: %v", err)
	}
	poller.store.Set(auth.ID, stale)
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}

	poller.poll(auth, polledAt)
	if got := transport.count(); got != 1 {
		t.Fatalf("requests for failed poll = %d, want 1", got)
	}
	got, ok := poller.store.Get(auth.ID)
	if !ok {
		t.Fatal("stale state removed after one failed poll")
	}
	if got.Session != stale.Session || got.Weekly != stale.Weekly || got.Fable != stale.Fable || !got.UpdatedAt.Equal(stale.UpdatedAt) {
		t.Fatalf("state after failed poll = %+v, want unchanged %+v", got, stale)
	}

	// Exercise the loaded-credential sweep while leaving the disabled second
	// credential unpolled, then observe the store boundary the product owns.
	poller.sweep(polledAt.Add(time.Minute))
	if quotaPollerInFlight(poller, neverPolled.ID) {
		t.Fatal("poll launched for disabled never-polled credential")
	}
	if got := transport.count(); got != 1 {
		t.Fatalf("requests after never-polled sweep = %d, want 1", got)
	}
	if _, ok := poller.store.Get(neverPolled.ID); ok {
		t.Fatal("never-polled credential unexpectedly has store state")
	}
}

func TestQuotaPollerPersistentFailureDegradesToNoState(t *testing.T) {
	store := NewQuotaUsageStore()
	p := newQuotaUsagePoller(nil, store)
	defer p.Stop()
	started := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	store.Set("acct-a", QuotaUsage{Session: window(10, started.Add(time.Hour))})
	p.schedule["acct-a"] = &quotaPollSchedule{}

	for i := 0; i < quotaFailureDegradeThreshold-1; i++ {
		p.schedule["acct-a"].inFlight = true
		p.finish("acct-a", started, started.Add(quotaFailureRetry), true)
		if _, ok := store.Get("acct-a"); !ok {
			t.Fatalf("state dropped after %d failures, before the threshold", i+1)
		}
	}
	p.schedule["acct-a"].inFlight = true
	p.finish("acct-a", started, started.Add(quotaFailureRetry), true)
	if _, ok := store.Get("acct-a"); ok {
		t.Fatal("state still present after the degradation threshold")
	}

	// A success resets the failure count.
	p.schedule["acct-a"].inFlight = true
	p.finish("acct-a", started, started.Add(time.Hour), false)
	if got := p.schedule["acct-a"].failures; got != 0 {
		t.Fatalf("failures = %d after success, want 0", got)
	}
}

func TestQuotaPollerAdoptCarriesScheduleAndState(t *testing.T) {
	started := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	oldStore := NewQuotaUsageStore()
	previous := newQuotaUsagePoller(nil, oldStore)
	defer previous.Stop()
	oldStore.Set("acct-a", QuotaUsage{Session: window(10, started.Add(time.Hour))})
	previous.schedule["acct-a"] = &quotaPollSchedule{
		nextPollAt: started.Add(45 * time.Minute), // e.g. an unexpired Retry-After
		inFlight:   true,                          // old poller's request; not adopted
		failures:   3,
	}

	newStore := NewQuotaUsageStore()
	next := newQuotaUsagePoller(nil, newStore)
	defer next.Stop()
	next.adopt(previous)

	sched, ok := next.schedule["acct-a"]
	if !ok {
		t.Fatal("schedule entry not adopted")
	}
	if !sched.nextPollAt.Equal(started.Add(45 * time.Minute)) {
		t.Fatalf("nextPollAt = %v, want the previous suppression window", sched.nextPollAt)
	}
	if sched.inFlight {
		t.Fatal("in-flight flag must not be adopted")
	}
	if sched.failures != 3 {
		t.Fatalf("failures = %d, want 3", sched.failures)
	}
	if _, ok := newStore.Get("acct-a"); !ok {
		t.Fatal("stored state not adopted")
	}
}
