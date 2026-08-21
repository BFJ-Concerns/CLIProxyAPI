// Fork addition (quota-aware routing): per-credential usage-window state and
// the reservation/eligibility arithmetic the quota-aware selector applies.
// New behaviour lives in fork-owned files; upstream code is touched only at
// the seams named in the patch.
package auth

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// QuotaWindow is one usage window's polled state: utilisation percent
// (0-100) and the moment the window resets.
type QuotaWindow struct {
	Percent  float64
	ResetsAt time.Time
}

// UtilizationAt returns the window's effective utilisation at now. A window
// whose recorded reset time has passed reads as replenished (0%) until the
// next successful poll.
func (w *QuotaWindow) UtilizationAt(now time.Time) float64 {
	if w == nil {
		return 0
	}
	if !w.ResetsAt.IsZero() && !w.ResetsAt.After(now) {
		return 0
	}
	return w.Percent
}

// HeadroomAt returns the window's remaining headroom in percentage points
// at now. A nil window imposes no cap and reads as full headroom.
func (w *QuotaWindow) HeadroomAt(now time.Time) float64 {
	headroom := 100 - w.UtilizationAt(now)
	if headroom < 0 {
		return 0
	}
	return headroom
}

// resetOrderingKey orders windows by urgency for earliest-reset-first
// selection. A window whose reset has already passed is replenished — a
// fresh budget with an unknown next reset — so it sorts after every window
// still counting down.
func (w *QuotaWindow) resetOrderingKey(now time.Time) time.Time {
	if w == nil || w.ResetsAt.IsZero() || !w.ResetsAt.After(now) {
		return now.Add(1000 * time.Hour)
	}
	return w.ResetsAt
}

// QuotaUsage is one credential's polled window state. A nil window means
// the usage endpoint reported no limit of that kind for the account; the
// missing dimension imposes no eligibility constraint, and a missing Fable
// window computes a zero reserve (nothing scoped to protect).
type QuotaUsage struct {
	Session *QuotaWindow
	Weekly  *QuotaWindow
	Fable   *QuotaWindow
	// UpdatedAt is the time of the last successful poll that produced
	// this state.
	UpdatedAt time.Time
}

// QuotaUsageStore holds the latest successfully polled usage state per
// auth ID. The poller writes; the selector reads. State survives poll
// failures — stale state keeps serving until replaced or removed.
type QuotaUsageStore struct {
	mu     sync.RWMutex
	states map[string]QuotaUsage
}

// NewQuotaUsageStore constructs an empty store.
func NewQuotaUsageStore() *QuotaUsageStore {
	return &QuotaUsageStore{states: make(map[string]QuotaUsage)}
}

// Set records the latest polled state for an auth ID.
func (s *QuotaUsageStore) Set(authID string, usage QuotaUsage) {
	if s == nil || authID == "" {
		return
	}
	s.mu.Lock()
	s.states[authID] = usage
	s.mu.Unlock()
}

// Get returns the recorded state for an auth ID and whether any exists.
func (s *QuotaUsageStore) Get(authID string) (QuotaUsage, bool) {
	if s == nil {
		return QuotaUsage{}, false
	}
	s.mu.RLock()
	usage, ok := s.states[authID]
	s.mu.RUnlock()
	return usage, ok
}

// IDs returns the auth IDs with recorded state.
func (s *QuotaUsageStore) IDs() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	ids := make([]string, 0, len(s.states))
	for id := range s.states {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	return ids
}

// Snapshot returns a copy of the recorded state per auth ID.
func (s *QuotaUsageStore) Snapshot() map[string]QuotaUsage {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	snapshot := make(map[string]QuotaUsage, len(s.states))
	for id, usage := range s.states {
		snapshot[id] = usage
	}
	s.mu.RUnlock()
	return snapshot
}

// Remove drops state for an auth ID (credential unloaded).
func (s *QuotaUsageStore) Remove(authID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.states, authID)
	s.mu.Unlock()
}

// QuotaPolicy carries the quota-aware strategy's parameters, already
// defaulted (internal/config owns the YAML surface and defaults).
type QuotaPolicy struct {
	// FableWeeklyRatio is the weekly points assumed consumed per Fable
	// headroom point served (the cap-ratio assumption, default 0.5).
	FableWeeklyRatio float64
	// ReserveMargin is added to every non-zero reserve, in percentage
	// points of the weekly window (default 10).
	ReserveMargin float64
	// SessionCeiling is the 5-hour-window utilisation percent at or above
	// which an account is skipped for new bindings (default 95).
	SessionCeiling float64
	// FableModelPrefixes classifies a request as Fable-class when the
	// requested model's canonical name matches any prefix
	// (case-insensitive, default ["claude-fable"]).
	FableModelPrefixes []string
}

// IsFableClass reports whether the requested model is Fable-class — i.e.
// draws on the account's model-scoped (Fable) weekly window.
func (p QuotaPolicy) IsFableClass(model string) bool {
	name := strings.ToLower(canonicalModelKey(model))
	if name == "" {
		return false
	}
	for _, prefix := range p.FableModelPrefixes {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix != "" && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// ReservedWeekly returns the weekly headroom (percentage points) reserved
// for Fable traffic on an account: FableWeeklyRatio × remaining Fable
// headroom, plus ReserveMargin — zero when the account has no Fable
// headroom (or no Fable window), and never more than the whole window.
func (p QuotaPolicy) ReservedWeekly(usage QuotaUsage, now time.Time) float64 {
	if usage.Fable == nil {
		return 0
	}
	fableHeadroom := usage.Fable.HeadroomAt(now)
	if fableHeadroom <= 0 {
		return 0
	}
	reserved := p.FableWeeklyRatio*fableHeadroom + p.ReserveMargin
	if reserved > 100 {
		return 100
	}
	if reserved < 0 {
		return 0
	}
	return reserved
}

// FreeWeekly returns the account's weekly headroom above its reserve —
// what a new non-Fable binding may draw on. Zero or negative means the
// account is fully reserved.
func (p QuotaPolicy) FreeWeekly(usage QuotaUsage, now time.Time) float64 {
	return usage.Weekly.HeadroomAt(now) - p.ReservedWeekly(usage, now)
}

// sessionOpen reports whether the account's 5-hour window is below the
// session ceiling. A missing session window imposes no constraint.
func (p QuotaPolicy) sessionOpen(usage QuotaUsage, now time.Time) bool {
	if usage.Session == nil {
		return true
	}
	return usage.Session.UtilizationAt(now) < p.SessionCeiling
}

// EligibleFable reports whether an account may take a new Fable-class
// binding: headroom on every window the request draws on (session, weekly,
// Fable) and the session window below the ceiling.
func (p QuotaPolicy) EligibleFable(usage QuotaUsage, now time.Time) bool {
	return p.sessionOpen(usage, now) &&
		usage.Session.HeadroomAt(now) > 0 &&
		usage.Weekly.HeadroomAt(now) > 0 &&
		usage.Fable.HeadroomAt(now) > 0
}

// EligibleNonFable reports whether an account may take a new non-Fable
// binding at all (the reservation gate is applied separately): headroom on
// the session and weekly windows and the session window below the ceiling.
func (p QuotaPolicy) EligibleNonFable(usage QuotaUsage, now time.Time) bool {
	return p.sessionOpen(usage, now) &&
		usage.Session.HeadroomAt(now) > 0 &&
		usage.Weekly.HeadroomAt(now) > 0
}

// quotaCandidate pairs a selectable auth with its polled state for one
// selection pass.
type quotaCandidate struct {
	auth  *Auth
	usage QuotaUsage
}

// sortByWeeklyReset orders candidates soonest-weekly-reset first,
// tie-breaking by auth ID for determinism. Both request classes order by
// the weekly window: Anthropic's model-scoped (Fable) window shares the
// weekly cadence but reports no reset time until the account has used
// some Fable, so ordering by the Fable reset would defer every untouched
// account behind one already counting down.
func sortByWeeklyReset(candidates []quotaCandidate, now time.Time) {
	sort.SliceStable(candidates, func(i, j int) bool {
		ri := candidates[i].usage.Weekly.resetOrderingKey(now)
		rj := candidates[j].usage.Weekly.resetOrderingKey(now)
		if ri.Equal(rj) {
			return candidates[i].auth.ID < candidates[j].auth.ID
		}
		return ri.Before(rj)
	})
}

// sortByLeastFableHeadroom orders candidates by remaining Fable headroom
// ascending (the reserve-dip order: burn where the least Fable capacity
// is left to strand), tie-breaking by auth ID.
func sortByLeastFableHeadroom(candidates []quotaCandidate, now time.Time) {
	sort.SliceStable(candidates, func(i, j int) bool {
		hi := candidates[i].usage.Fable.HeadroomAt(now)
		hj := candidates[j].usage.Fable.HeadroomAt(now)
		if hi == hj {
			return candidates[i].auth.ID < candidates[j].auth.ID
		}
		return hi < hj
	})
}
