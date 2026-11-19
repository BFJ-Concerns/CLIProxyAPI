package auth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

func TestQuotaDecisionSurvivesProductionLogFormatter(t *testing.T) {
	output := captureQuotaProductionLogs(t)

	// Fixed reset times far ahead keep the rendered windows stable while
	// Pick evaluates them against the real clock.
	base := time.Date(2099, time.August, 21, 18, 50, 0, 0, time.UTC)
	selector := NewQuotaAwareSelector(defaultTestPolicy())
	alpha := subscriptionAuth("account-alpha")
	beta := subscriptionAuth("account-beta")
	// alpha is evaluated first, but beta's weekly window resets sooner and
	// it has free headroom, so beta is the policy's choice.
	selector.store.Set(alpha.ID, QuotaUsage{
		Session: &QuotaWindow{Percent: 12, ResetsAt: base.Add(2 * time.Hour)},
		Weekly:  &QuotaWindow{Percent: 35, ResetsAt: base.Add(72 * time.Hour)},
		Fable:   &QuotaWindow{Percent: 40, ResetsAt: base.Add(24 * time.Hour)},
	})
	selector.store.Set(beta.ID, QuotaUsage{
		Session: &QuotaWindow{Percent: 20, ResetsAt: base.Add(3 * time.Hour)},
		Weekly:  &QuotaWindow{Percent: 50, ResetsAt: base.Add(48 * time.Hour)},
		Fable:   &QuotaWindow{Percent: 90, ResetsAt: base.Add(36 * time.Hour)},
	})

	ctx := internallogging.WithRequestID(context.Background(), "decision")
	chosen, errPick := selector.Pick(ctx, "claude", "claude-sonnet", cliproxyexecutor.Options{}, []*Auth{alpha, beta})
	if errPick != nil || chosen == nil || chosen.ID != beta.ID {
		t.Fatalf("Pick = %v, %v; want account-beta", chosen, errPick)
	}

	line := output.String()
	for _, want := range []string{
		"[decision]",
		"class=non-fable",
		"model=claude-sonnet",
		"candidates=account-alpha session=12.0%@2099-08-21T20:50:00Z weekly=35.0%@2099-08-24T18:50:00Z free=25.0 fable=40.0%@2099-08-22T18:50:00Z eligible=true; account-beta session=20.0%@2099-08-21T21:50:00Z weekly=50.0%@2099-08-23T18:50:00Z free=35.0 fable=90.0%@2099-08-23T06:50:00Z eligible=true",
		"chosen=account-beta",
		`reason="earliest-weekly-reset"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatted decision line %q missing %q", line, want)
		}
	}
}

func TestSuccessfulQuotaPollSurvivesProductionLogFormatter(t *testing.T) {
	output := captureQuotaProductionLogs(t)
	now := time.Date(2030, time.August, 21, 18, 50, 0, 0, time.UTC)
	store := NewQuotaUsageStore()
	poller := newQuotaUsagePoller(nil, store)
	t.Cleanup(poller.Stop)
	poller.schedule["account-gamma"] = &quotaPollSchedule{inFlight: true}
	poller.clients[""] = &http.Client{Transport: quotaLoggingRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"limits": [
					{"kind":"session","percent":12.5,"resets_at":"2030-08-21T20:50:00Z"},
					{"kind":"weekly_all","percent":45,"resets_at":"2030-08-24T18:50:00Z"},
					{"kind":"weekly_scoped","percent":7.5,"resets_at":"2030-08-23T06:50:00Z"}
				]
			}`)),
		}, nil
	})}

	poller.poll(&Auth{ID: "account-gamma", Metadata: map[string]any{"access_token": "test-token"}}, now)

	line := output.String()
	for _, want := range []string{
		"quota usage polled",
		"auth=account-gamma",
		"session=12.5%@2030-08-21T20:50:00Z",
		"weekly=45.0%@2030-08-24T18:50:00Z",
		"fable=7.5%@2030-08-23T06:50:00Z",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatted poll line %q missing %q", line, want)
		}
	}
}

type quotaLoggingRoundTripper func(*http.Request) (*http.Response, error)

func (f quotaLoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func captureQuotaProductionLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logger := log.StandardLogger()
	previousOutput := logger.Out
	previousFormatter := logger.Formatter
	previousLevel := logger.Level
	previousReportCaller := logger.ReportCaller
	t.Cleanup(func() {
		logger.SetOutput(previousOutput)
		logger.SetFormatter(previousFormatter)
		logger.SetLevel(previousLevel)
		logger.SetReportCaller(previousReportCaller)
	})

	output := &bytes.Buffer{}
	logger.SetOutput(output)
	logger.SetFormatter(&internallogging.LogFormatter{})
	logger.SetLevel(log.InfoLevel)
	logger.SetReportCaller(false)

	return output
}
