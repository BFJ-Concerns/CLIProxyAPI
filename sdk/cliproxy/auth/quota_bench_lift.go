// Fork addition (quota-aware routing): a successful usage poll lifts a
// Claude 429 bench once the window the bench was recorded for has headroom
// again. A bench otherwise lasts until the deadline the rejection named, so
// a window that resets early (or is reset by Anthropic) would keep the
// account out of rotation while the polled state already shows it free.
package auth

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// liftWindowBenches lifts every 429 bench on the credential — the
// credential-wide one and each model-scoped one — whose rejected windows
// all read below the utilisation they were rejected at in the freshly
// polled usage. A bench whose windows cannot be judged from the poll (no
// recorded rejection signals, a window the poll does not report) and
// force cooldowns are left to run out. polledAt is when the poll started:
// a bench recorded after it may come from a request that exhausted the
// window after the poll read it, so it is left for the next poll to judge.
func (m *Manager) liftWindowBenches(authID string, usage QuotaUsage, polledAt, now time.Time) {
	if m == nil || strings.TrimSpace(authID) == "" {
		return
	}
	var lifted []string
	_, errCommit := m.commitCooldownEdit(context.Background(), authID, func(auth *Auth, _ time.Time) bool {
		if auth.Disabled || auth.Status == StatusDisabled || hasUnauthorizedAuthFailure(auth) {
			return false
		}
		if credentialBenchLiftable(auth, usage, polledAt, now) {
			// The same unwinding a successful request performs: the
			// credential layer is dropped and each model's independent
			// bench is reinstated for the loop below to judge on its own
			// windows.
			clearAuthStateOnSuccess(auth, now)
			lifted = append(lifted, "credential")
		}
		for modelKey, state := range auth.ModelStates {
			if modelBenchLiftable(state, usage, polledAt, now) {
				resetModelState(state, now)
				lifted = append(lifted, modelKey)
			}
		}
		if len(lifted) == 0 {
			return false
		}
		updateAggregatedAvailability(auth, now)
		return true
	})
	if errCommit != nil {
		logEntryWithRequestID(context.Background()).WithField("auth_id", authID).Warnf("failed to persist lifted quota bench: %v", errCommit)
	}
	if len(lifted) > 0 {
		logEntryWithRequestID(context.Background()).WithField("auth_id", authID).Infof("quota poll shows headroom in the benched window; lifted benches=%s", strings.Join(lifted, ","))
	}
}

// credentialBenchLiftable reports whether the credential carries a live
// credential-wide 429 bench, not held by a force cooldown or a non-quota
// retry deadline, whose rejected windows all have headroom again. The
// credential carries no bench-recording time of its own, so any update to
// it after the poll started holds the bench for the next poll.
func credentialBenchLiftable(auth *Auth, usage QuotaUsage, polledAt, now time.Time) bool {
	if !auth.Quota.Exceeded || !auth.Quota.NextRecoverAt.After(now) || auth.UpdatedAt.After(polledAt) {
		return false
	}
	if auth.Quota.Reason != "credential_quota" && len(auth.ModelStates) > 0 {
		return false
	}
	if effectiveAuthForceUntil(auth).After(now) || auth.credQuotaForceUntil.After(now) ||
		auth.quotaForceCooldownUntil.After(now) || auth.credentialRetryAfter.After(now) || auth.nonQuotaCooldownUntil.After(now) {
		return false
	}
	return benchWindowsHaveHeadroom(auth.Quota.cooldownSignals, usage, now)
}

// modelBenchLiftable reports whether a model state carries its own live 429
// bench (not one propagated from a still-live credential bench, and not a
// force cooldown), recorded before the poll started, whose rejected
// windows all have headroom again.
func modelBenchLiftable(state *ModelState, usage QuotaUsage, polledAt, now time.Time) bool {
	if state == nil || state.Status == StatusDisabled {
		return false
	}
	if state.credentialQuotaPropagated || state.Quota.Reason == "credential_quota" {
		return false
	}
	if !state.Quota.Exceeded || !state.Quota.NextRecoverAt.After(now) || state.UpdatedAt.After(polledAt) {
		return false
	}
	if effectiveModelForceUntil(state).After(now) || state.quotaForceCooldownUntil.After(now) || state.nonQuotaCooldownUntil.After(now) {
		return false
	}
	return benchWindowsHaveHeadroom(state.Quota.cooldownSignals, usage, now)
}

// benchWindowsHaveHeadroom judges the rejection headers a bench was
// recorded with against polled usage: every window the rejection named
// must now read below the utilisation it was rejected at (100% where the
// rejection carried no reading). A rejection naming no window, or a window
// the poll does not report, cannot be judged and is never lifted.
func benchWindowsHaveHeadroom(signals map[string]string, usage QuotaUsage, now time.Time) bool {
	if len(signals) == 0 {
		return false
	}
	headers := make(http.Header, len(signals))
	for key, value := range signals {
		headers.Set(key, value)
	}
	windows := rejectedClaudeUnifiedWindows(headers)
	if len(windows) == 0 {
		return false
	}
	for _, name := range windows {
		polled := polledWindowForClaudeRejection(usage, name)
		if polled == nil {
			return false
		}
		if polled.UtilizationAt(now) >= rejectedWindowLevel(headers, name) {
			return false
		}
	}
	return true
}

// polledWindowForClaudeRejection maps a rejected Anthropic unified window
// to the usage endpoint's reading of the same window.
func polledWindowForClaudeRejection(usage QuotaUsage, window string) *QuotaWindow {
	switch window {
	case "5h":
		return usage.Session
	case "7d":
		return usage.Weekly
	case "7d_oi":
		return usage.Fable
	default:
		return nil
	}
}

// rejectedWindowLevel returns the utilisation percent a window was rejected
// at, from its utilisation header (a fraction), capped at 100; 100 when the
// rejection carried no usable reading.
func rejectedWindowLevel(headers http.Header, window string) float64 {
	raw := strings.TrimSpace(headers.Get(claudeUnifiedWindowHeader(window, "Utilization")))
	fraction, errParse := strconv.ParseFloat(raw, 64)
	if raw == "" || errParse != nil || math.IsNaN(fraction) || math.IsInf(fraction, 0) || fraction <= 0 {
		return 100
	}
	return math.Min(fraction*100, 100)
}
