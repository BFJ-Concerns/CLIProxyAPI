// Fork addition (quota-aware routing): the selector strategy. It decides
// only cold session bindings and failover — session affinity wraps it
// exactly as it wraps the built-in strategies — and governs only Claude
// subscription (OAuth) credentials; everything else delegates to the
// wrapped fallback strategy.
package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// QuotaAwareSelector picks the credential whose perishable quota would
// otherwise be wasted first: Fable-class requests go to the eligible
// account whose weekly window resets soonest; other requests go to the
// eligible account with free (unreserved) weekly headroom whose weekly
// window resets soonest, dipping into reserves only when every account is
// reserved — on the account with the least Fable headroom left to strand.
type QuotaAwareSelector struct {
	policy   QuotaPolicy
	store    *QuotaUsageStore
	fallback Selector

	mu     sync.Mutex
	poller *quotaUsagePoller
}

// NewQuotaAwareSelector constructs the strategy with normalised policy
// parameters (internal/config owns YAML parsing and defaulting).
func NewQuotaAwareSelector(policy QuotaPolicy) *QuotaAwareSelector {
	return &QuotaAwareSelector{
		policy:   policy,
		store:    NewQuotaUsageStore(),
		fallback: &RoundRobinSelector{},
	}
}

// isClaudeSubscriptionAuth reports whether the credential is a Claude
// subscription (OAuth) account — the population the quota policy governs.
func isClaudeSubscriptionAuth(auth *Auth) bool {
	if auth == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(auth.Provider), "claude") && auth.AuthKind() == AuthKindOAuth
}

// Pick implements Selector.
func (s *QuotaAwareSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	now := time.Now()
	available, err := getAvailableAuths(auths, provider, model, now)
	if err != nil {
		return nil, err
	}

	var subscriptions []*Auth
	for _, candidate := range available {
		if isClaudeSubscriptionAuth(candidate) {
			subscriptions = append(subscriptions, candidate)
		}
	}
	// Credentials of other providers keep upstream's existing routing.
	if len(subscriptions) == 0 {
		return s.fallback.Pick(ctx, provider, model, opts, available)
	}

	fableClass := s.policy.IsFableClass(model)

	var withState []quotaCandidate
	var noState []*Auth
	for _, candidate := range subscriptions {
		if usage, ok := s.store.Get(candidate.ID); ok {
			withState = append(withState, quotaCandidate{auth: candidate, usage: usage})
		} else {
			noState = append(noState, candidate)
		}
	}

	chosen, reason := s.pickByPolicy(withState, fableClass, now)

	// A credential with no successfully polled state yet is chosen only
	// when no eligible credential with state exists.
	if chosen == nil && len(noState) > 0 {
		if pick, errPick := s.fallback.Pick(ctx, provider, model, opts, noState); errPick == nil && pick != nil {
			chosen, reason = pick, "no-polled-state"
		}
	}

	// Availability beats the policy: when nothing is admissible under the
	// quota rules but upstream availability let candidates through, keep
	// serving rather than reject (the 429 backstop remains beneath).
	if chosen == nil {
		pick, errPick := s.fallback.Pick(ctx, provider, model, opts, available)
		if errPick != nil {
			return nil, errPick
		}
		chosen, reason = pick, "policy-exhausted-fallback"
	}

	s.logDecision(ctx, model, fableClass, withState, noState, chosen, reason, now)
	return chosen, nil
}

// pickByPolicy applies the routing policy over candidates with polled
// state and returns the choice with the reason tag for the decision line.
func (s *QuotaAwareSelector) pickByPolicy(candidates []quotaCandidate, fableClass bool, now time.Time) (*Auth, string) {
	if fableClass {
		var eligible []quotaCandidate
		for _, candidate := range candidates {
			if s.policy.EligibleFable(candidate.usage, now) {
				eligible = append(eligible, candidate)
			}
		}
		if len(eligible) == 0 {
			return nil, ""
		}
		sortByWeeklyReset(eligible, now)
		return eligible[0].auth, "earliest-weekly-reset"
	}

	var eligible []quotaCandidate
	var free []quotaCandidate
	for _, candidate := range candidates {
		if !s.policy.EligibleNonFable(candidate.usage, now) {
			continue
		}
		eligible = append(eligible, candidate)
		if s.policy.FreeWeekly(candidate.usage, now) > 0 {
			free = append(free, candidate)
		}
	}
	if len(free) > 0 {
		sortByWeeklyReset(free, now)
		return free[0].auth, "earliest-weekly-reset"
	}
	// Every account is reserved: availability beats reservation. Dip where
	// it strands the least Fable capacity.
	if len(eligible) > 0 {
		sortByLeastFableHeadroom(eligible, now)
		return eligible[0].auth, "reserve-dip"
	}
	return nil, ""
}

// logDecision emits the cold-binding decision line: request class, the
// full candidate set as evaluated (with each account's window state), and
// the choice.
func (s *QuotaAwareSelector) logDecision(ctx context.Context, model string, fableClass bool, withState []quotaCandidate, noState []*Auth, chosen *Auth, reason string, now time.Time) {
	class := "non-fable"
	if fableClass {
		class = "fable"
	}
	evaluated := make([]string, 0, len(withState)+len(noState))
	for _, candidate := range withState {
		evaluated = append(evaluated, describeQuotaCandidate(s.policy, candidate, fableClass, now))
	}
	for _, auth := range noState {
		evaluated = append(evaluated, fmt.Sprintf("%s state=none", auth.ID))
	}
	chosenID := ""
	if chosen != nil {
		chosenID = chosen.ID
	}
	logEntryWithRequestID(ctx).Infof(
		"quota-aware cold-binding decision class=%s model=%s candidates=%s chosen=%s reason=%q",
		class,
		model,
		strings.Join(evaluated, "; "),
		chosenID,
		reason,
	)
}

// describeQuotaCandidate renders one account's evaluated state for the
// decision line.
func describeQuotaCandidate(policy QuotaPolicy, candidate quotaCandidate, fableClass bool, now time.Time) string {
	usage := candidate.usage
	eligible := policy.EligibleNonFable(usage, now)
	if fableClass {
		eligible = policy.EligibleFable(usage, now)
	}
	return fmt.Sprintf("%s session=%s weekly=%s free=%.1f fable=%s eligible=%t",
		candidate.auth.ID,
		describeQuotaWindow(usage.Session, now),
		describeQuotaWindow(usage.Weekly, now),
		policy.FreeWeekly(usage, now),
		describeQuotaWindow(usage.Fable, now),
		eligible,
	)
}

// describeQuotaWindow renders one window as "<percent>%@<reset>" with the
// replenishment reading applied.
func describeQuotaWindow(w *QuotaWindow, now time.Time) string {
	if w == nil {
		return "none"
	}
	reset := "unknown"
	if !w.ResetsAt.IsZero() {
		reset = w.ResetsAt.Format(time.RFC3339)
	}
	return fmt.Sprintf("%.1f%%@%s", w.UtilizationAt(now), reset)
}

// bindPoller installs (replacing and stopping any previous) the poller
// that feeds this selector's store.
func (s *QuotaAwareSelector) bindPoller(p *quotaUsagePoller) {
	s.mu.Lock()
	previous := s.poller
	s.poller = p
	s.mu.Unlock()
	if previous != nil {
		previous.Stop()
	}
}

// Stop implements StoppableSelector: it stops the bound poller. Note the
// session-affinity wrapper does not forward Stop to its fallback, so
// selector replacement is handled by BindQuotaPoller at the install seams
// rather than by this method alone.
func (s *QuotaAwareSelector) Stop() {
	s.bindPoller(nil)
}
