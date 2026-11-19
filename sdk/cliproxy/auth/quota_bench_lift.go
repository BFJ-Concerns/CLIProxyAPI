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

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

// liftWindowBenches lifts every 429 bench on the credential — the
// credential-wide one and each model-scoped one — whose rejected windows
// all read below the utilisation they were rejected at in the freshly
// polled usage. A bench whose windows cannot be judged from the poll (no
// recorded rejection signals, a window the poll does not report) and
// force cooldowns are left to run out.
func (m *Manager) liftWindowBenches(authID string, usage QuotaUsage, now time.Time) {
	if m == nil || strings.TrimSpace(authID) == "" {
		return
	}
	releaseMutation := m.lockAuthMutation(authID)
	m.mu.Lock()
	auth, ok := m.auths[authID]
	if !ok || auth == nil || auth.Disabled || auth.Status == StatusDisabled || hasUnauthorizedAuthFailure(auth) {
		m.mu.Unlock()
		releaseMutation()
		return
	}

	trackCooldownState := m.cooldownStore != nil
	var cooldownRecordsBefore []CooldownStateRecord
	if trackCooldownState {
		cooldownRecordsBefore = m.cooldownStateRecordsForAuthLocked(auth, now)
	}

	var lifted []string
	if credentialBenchLiftable(auth, usage, now) {
		// The same unwinding a successful request performs: the credential
		// layer is dropped and each model's independent bench is reinstated
		// for the loop below to judge on its own windows.
		clearAuthStateOnSuccess(auth, now)
		lifted = append(lifted, "credential")
	}
	for modelKey, state := range auth.ModelStates {
		if modelBenchLiftable(state, usage, now) {
			resetModelState(state, now)
			lifted = append(lifted, modelKey)
		}
	}
	if len(lifted) == 0 {
		m.mu.Unlock()
		releaseMutation()
		return
	}

	updateAggregatedAvailability(auth, now)
	if !hasModelError(auth, now) {
		auth.LastError = nil
		auth.StatusMessage = ""
		auth.Status = StatusActive
	}
	auth.Generation++
	auth.UpdatedAt = now
	snapshot := auth.Clone()
	cooldownStateChanged := false
	if trackCooldownState {
		cooldownStateChanged = !cooldownStateRecordsEqual(cooldownRecordsBefore, m.cooldownStateRecordsForAuthLocked(auth, now))
	}
	errPersist := m.persistLocked(context.Background(), auth)
	m.mu.Unlock()
	releaseMutation()

	if errPersist != nil {
		logEntryWithRequestID(context.Background()).WithField("auth_id", authID).Warnf("failed to persist lifted quota bench: %v", errPersist)
	}
	if cooldownStateChanged {
		m.persistCooldownStates(context.Background())
	}
	supportedModels, regEpoch := registry.GetGlobalRegistry().GetModelsAndEpochForClient(authID)
	projections := make([]registry.ClientModelProjection, 0, len(supportedModels))
	for _, supportedModel := range supportedModels {
		if supportedModel == nil || strings.TrimSpace(supportedModel.ID) == "" {
			continue
		}
		projections = append(projections, m.clientModelProjectionForAuth(snapshot, supportedModel.ID, now))
	}
	registry.GetGlobalRegistry().ApplyClientModelProjections(authID, regEpoch, snapshot.Generation, projections)
	if m.scheduler != nil {
		m.scheduler.upsertAuth(snapshot)
	}
	logEntryWithRequestID(context.Background()).WithField("auth_id", authID).Infof("quota poll shows headroom in the benched window; lifted benches=%s", strings.Join(lifted, ","))
}

// credentialBenchLiftable reports whether the credential carries a live
// credential-wide 429 bench, not held by a force cooldown or a non-quota
// retry deadline, whose rejected windows all have headroom again.
func credentialBenchLiftable(auth *Auth, usage QuotaUsage, now time.Time) bool {
	if !auth.Quota.Exceeded || !auth.Quota.NextRecoverAt.After(now) {
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
// force cooldown) whose rejected windows all have headroom again.
func modelBenchLiftable(state *ModelState, usage QuotaUsage, now time.Time) bool {
	if state == nil || state.Status == StatusDisabled {
		return false
	}
	if state.credentialQuotaPropagated || state.Quota.Reason == "credential_quota" {
		return false
	}
	if !state.Quota.Exceeded || !state.Quota.NextRecoverAt.After(now) {
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
