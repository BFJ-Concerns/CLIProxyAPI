package auth

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

func captureQuotaReadings(t *testing.T) func() []QuotaReading {
	t.Helper()
	var mu sync.Mutex
	var readings []QuotaReading
	SetQuotaReadingSink(func(_ *Auth, reading QuotaReading) {
		mu.Lock()
		readings = append(readings, reading)
		mu.Unlock()
	})
	t.Cleanup(func() { SetQuotaReadingSink(nil) })
	return func() []QuotaReading {
		mu.Lock()
		defer mu.Unlock()
		return append([]QuotaReading(nil), readings...)
	}
}

func TestQuotaPollReportsSuccessfulReadingVerbatim(t *testing.T) {
	body := `{"limits":[{"kind":"session","percent":12,"resets_at":"2026-10-01T15:00:00Z"}],"seven_day_breakdown":{}}`
	poller, transport, auth := newCountedQuotaPoller(t, http.StatusOK, body)
	readings := captureQuotaReadings(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}

	poller.poll(auth, now)
	waitForQuotaPollerRequest(t, transport.issued)

	got := readings()
	if len(got) != 1 {
		t.Fatalf("readings = %d, want 1", len(got))
	}
	r := got[0]
	if r.Status != http.StatusOK || r.Failed || r.Throttled || string(r.Body) != body || !r.PolledAt.Equal(now) {
		t.Fatalf("reading = %+v", r)
	}
}

func TestQuotaPollReportsThrottledAndFailedReadingsWithoutBody(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	poller, transport, auth := newCountedQuotaPoller(t, http.StatusTooManyRequests, `{"error":"rate_limited"}`)
	transport.header = http.Header{"Retry-After": []string{"600"}}
	readings := captureQuotaReadings(t)
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}
	poller.poll(auth, now)
	waitForQuotaPollerRequest(t, transport.issued)
	got := readings()
	if len(got) != 1 || !got[0].Throttled || got[0].Failed || got[0].Status != http.StatusTooManyRequests || got[0].Body != nil || !got[0].RetryAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("throttled reading = %+v", got)
	}

	poller, transport, auth = newCountedQuotaPoller(t, http.StatusInternalServerError, `oops`)
	readings = captureQuotaReadings(t)
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}
	poller.poll(auth, now)
	waitForQuotaPollerRequest(t, transport.issued)
	got = readings()
	if len(got) != 1 || !got[0].Failed || got[0].Status != http.StatusInternalServerError || got[0].Body != nil {
		t.Fatalf("failed reading = %+v", got)
	}
}

func TestQuotaPollReportsMissingTokenAsFailedReading(t *testing.T) {
	poller, _, auth := newCountedQuotaPoller(t, http.StatusOK, `{}`)
	auth.Metadata = map[string]any{}
	readings := captureQuotaReadings(t)
	poller.schedule[auth.ID] = &quotaPollSchedule{inFlight: true}
	poller.poll(auth, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	got := readings()
	if len(got) != 1 || !got[0].Failed || got[0].Status != 0 || got[0].Err == "" {
		t.Fatalf("missing-token reading = %+v", got)
	}
}
