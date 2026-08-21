package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func subscriptionAuth(id string) *Auth {
	return &Auth{
		ID:         id,
		Provider:   "claude",
		Attributes: map[string]string{"auth_kind": "oauth"},
	}
}

func newTestQuotaSelector() *QuotaAwareSelector {
	return NewQuotaAwareSelector(defaultTestPolicy())
}

func pick(t *testing.T, s *QuotaAwareSelector, model string, auths []*Auth) *Auth {
	t.Helper()
	chosen, err := s.Pick(context.Background(), "claude", model, cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if chosen == nil {
		t.Fatal("Pick returned nil auth without error")
	}
	return chosen
}

func TestQuotaPickFableClassEarliestWeeklyReset(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	a := subscriptionAuth("acct-a")
	b := subscriptionAuth("acct-b")
	c := subscriptionAuth("acct-c")

	// b resets soonest but is ineligible (weekly exhausted: the headline
	// stranding shape); of the eligible pair, c's weekly window resets
	// before a's. c's Fable window has never been used, so — as Anthropic
	// reports it — it carries no reset time; ordering by the Fable reset
	// would wrongly defer c behind a.
	s.store.Set("acct-a", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(10, now.Add(72*time.Hour)),
		Fable:   window(10, now.Add(72*time.Hour)),
	})
	s.store.Set("acct-b", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(100, now.Add(6*time.Hour)),
		Fable:   window(10, now.Add(6*time.Hour)),
	})
	s.store.Set("acct-c", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(0, now.Add(48*time.Hour)),
		Fable:   window(0, time.Time{}),
	})

	chosen := pick(t, s, "claude-fable-5", []*Auth{a, b, c})
	if chosen.ID != "acct-c" {
		t.Fatalf("chose %s, want acct-c (earliest eligible weekly reset)", chosen.ID)
	}
}

func TestQuotaPickFableClassAllWeeklyExhaustedFallsBack(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	a := subscriptionAuth("acct-a")
	b := subscriptionAuth("acct-b")

	// Neither account is policy-eligible because both weekly windows are
	// exhausted. Availability still wins: fallback routing starts at a,
	// even though b has the earlier Fable reset.
	s.store.Set("acct-a", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(100, now.Add(24*time.Hour)),
		Fable:   window(10, now.Add(72*time.Hour)),
	})
	s.store.Set("acct-b", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(100, now.Add(24*time.Hour)),
		Fable:   window(10, now.Add(6*time.Hour)),
	})

	chosen := pick(t, s, "claude-fable-5", []*Auth{a, b})
	if chosen.ID != "acct-a" {
		t.Fatalf("chose %s, want acct-a (all weekly windows exhausted, so availability fallback applies)", chosen.ID)
	}
}

func TestQuotaPickNonFableRespectsReservationGate(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	a := subscriptionAuth("acct-a")
	b := subscriptionAuth("acct-b")

	// a's weekly resets sooner, but its whole weekly headroom is inside
	// its reserve (fable headroom 100 -> reserve 60; weekly headroom 40).
	// b has free headroom, so the gate must route to b.
	s.store.Set("acct-a", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(60, now.Add(24*time.Hour)),
		Fable:   window(0, now.Add(120*time.Hour)),
	})
	s.store.Set("acct-b", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(10, now.Add(96*time.Hour)),
		Fable:   window(90, now.Add(120*time.Hour)), // reserve 0.5*10+10 = 15; free = 75
	})

	chosen := pick(t, s, "claude-sonnet-5", []*Auth{a, b})
	if chosen.ID != "acct-b" {
		t.Fatalf("chose %s, want acct-b (only account with free weekly headroom)", chosen.ID)
	}
}

func TestQuotaPickNonFableUsesFableExhaustionToReleaseReserve(t *testing.T) {
	now := time.Now()

	t.Run("some accounts", func(t *testing.T) {
		s := newTestQuotaSelector()
		spent := subscriptionAuth("acct-spent")
		reserved := subscriptionAuth("acct-reserved")

		// Both have 40 weekly points left. The spent account reserves zero
		// because its Fable headroom is exhausted; the Fable-rich account's
		// entire weekly headroom remains reserved despite resetting sooner.
		s.store.Set("acct-spent", QuotaUsage{
			Session: window(10, now.Add(time.Hour)),
			Weekly:  window(60, now.Add(72*time.Hour)),
			Fable:   window(100, now.Add(120*time.Hour)),
		})
		s.store.Set("acct-reserved", QuotaUsage{
			Session: window(10, now.Add(time.Hour)),
			Weekly:  window(60, now.Add(12*time.Hour)),
			Fable:   window(40, now.Add(120*time.Hour)),
		})

		chosen := pick(t, s, "claude-sonnet-5", []*Auth{reserved, spent})
		if chosen.ID != "acct-spent" {
			t.Fatalf("chose %s, want acct-spent (Fable exhaustion releases its weekly reserve)", chosen.ID)
		}
	})

	t.Run("all accounts", func(t *testing.T) {
		s := newTestQuotaSelector()
		a := subscriptionAuth("acct-a")
		b := subscriptionAuth("acct-b")

		// With Fable exhausted everywhere, every reserve is zero and normal
		// earliest-weekly-reset ordering applies even when a has less weekly
		// headroom than the margin. If exhaustion incorrectly left a
		// 10-point reserve, only b would have free weekly headroom.
		s.store.Set("acct-a", QuotaUsage{
			Session: window(10, now.Add(time.Hour)),
			Weekly:  window(95, now.Add(12*time.Hour)),
			Fable:   window(100, now.Add(120*time.Hour)),
		})
		s.store.Set("acct-b", QuotaUsage{
			Session: window(10, now.Add(time.Hour)),
			Weekly:  window(80, now.Add(72*time.Hour)),
			Fable:   window(100, now.Add(120*time.Hour)),
		})

		chosen := pick(t, s, "claude-sonnet-5", []*Auth{a, b})
		if chosen.ID != "acct-a" {
			t.Fatalf("chose %s, want acct-a (all reserves released; earliest weekly reset)", chosen.ID)
		}
	})
}

func TestQuotaPickNonFableEarliestWeeklyResetAmongFree(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	a := subscriptionAuth("acct-a")
	b := subscriptionAuth("acct-b")

	// Both free; a's weekly window resets sooner, so its perishable
	// headroom is used first.
	s.store.Set("acct-a", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(30, now.Add(12*time.Hour)),
		Fable:   window(90, now.Add(120*time.Hour)),
	})
	s.store.Set("acct-b", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(30, now.Add(96*time.Hour)),
		Fable:   window(90, now.Add(120*time.Hour)),
	})

	chosen := pick(t, s, "claude-sonnet-5", []*Auth{a, b})
	if chosen.ID != "acct-a" {
		t.Fatalf("chose %s, want acct-a (earliest weekly reset among free)", chosen.ID)
	}
}

func TestQuotaPickAllReservedDipsWhereLeastFableHeadroom(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	a := subscriptionAuth("acct-a")
	b := subscriptionAuth("acct-b")

	// Fresh accounts reserve heavily: neither has free weekly headroom.
	// Availability beats reservation; the dip lands on b, which has
	// less Fable headroom left to strand.
	s.store.Set("acct-a", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(50, now.Add(24*time.Hour)),
		Fable:   window(20, now.Add(120*time.Hour)), // reserve 0.5*80+10 = 50 >= headroom 50
	})
	s.store.Set("acct-b", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(60, now.Add(24*time.Hour)),
		Fable:   window(40, now.Add(120*time.Hour)), // reserve 0.5*60+10 = 40 >= headroom 40
	})

	chosen := pick(t, s, "claude-sonnet-5", []*Auth{a, b})
	if chosen.ID != "acct-b" {
		t.Fatalf("chose %s, want acct-b (least Fable headroom to strand)", chosen.ID)
	}
}

func TestQuotaPickSkipsSessionSaturatedAccounts(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	a := subscriptionAuth("acct-a")
	b := subscriptionAuth("acct-b")

	// a is otherwise the policy's first choice but sits at the session
	// ceiling; the binding must land on b.
	s.store.Set("acct-a", QuotaUsage{
		Session: window(96, now.Add(time.Hour)),
		Weekly:  window(10, now.Add(12*time.Hour)),
		Fable:   window(10, now.Add(24*time.Hour)),
	})
	s.store.Set("acct-b", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(10, now.Add(96*time.Hour)),
		Fable:   window(10, now.Add(120*time.Hour)),
	})

	for _, model := range []string{"claude-fable-5", "claude-sonnet-5"} {
		chosen := pick(t, s, model, []*Auth{a, b})
		if chosen.ID != "acct-b" {
			t.Fatalf("model %s: chose %s, want acct-b (acct-a at session ceiling)", model, chosen.ID)
		}
	}
}

func TestQuotaPickReplenishesPassedResetBeforeSelection(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	replenished := subscriptionAuth("acct-replenished")
	exhausted := subscriptionAuth("acct-exhausted")

	// Both recorded weekly utilisation as exhausted, but only the first
	// reset has passed. Pick must read that cached window as replenished.
	s.store.Set("acct-replenished", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(100, now.Add(-time.Minute)),
		Fable:   window(10, now.Add(48*time.Hour)),
	})
	s.store.Set("acct-exhausted", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(100, now.Add(24*time.Hour)),
		Fable:   window(10, now.Add(6*time.Hour)),
	})

	chosen := pick(t, s, "claude-fable-5", []*Auth{exhausted, replenished})
	if chosen.ID != "acct-replenished" {
		t.Fatalf("chose %s, want acct-replenished (passed weekly reset restores eligibility)", chosen.ID)
	}
}

func TestQuotaPickContinuesUsingOldRetainedState(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	unknown := subscriptionAuth("acct-a-unknown")
	old := subscriptionAuth("acct-z-old")

	// The successful poll is old but its retained windows remain current.
	// It must still outrank a credential that has never produced state.
	s.store.Set("acct-z-old", QuotaUsage{
		Session:   window(10, now.Add(time.Hour)),
		Weekly:    window(10, now.Add(24*time.Hour)),
		Fable:     window(10, now.Add(48*time.Hour)),
		UpdatedAt: now.Add(-24 * time.Hour),
	})

	chosen := pick(t, s, "claude-fable-5", []*Auth{unknown, old})
	if chosen.ID != "acct-z-old" {
		t.Fatalf("chose %s, want acct-z-old (old retained state still drives selection)", chosen.ID)
	}
}

func TestQuotaPickNoStateUsedOnlyWithoutEligibleState(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	known := subscriptionAuth("acct-known")
	unknown := subscriptionAuth("acct-unknown")

	s.store.Set("acct-known", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(10, now.Add(24*time.Hour)),
		Fable:   window(10, now.Add(24*time.Hour)),
	})

	// While an eligible polled credential exists, the never-polled one is
	// not chosen.
	if chosen := pick(t, s, "claude-fable-5", []*Auth{known, unknown}); chosen.ID != "acct-known" {
		t.Fatalf("chose %s, want acct-known while it is eligible", chosen.ID)
	}

	// Once the polled credential is ineligible, the never-polled one is
	// the right choice.
	s.store.Set("acct-known", QuotaUsage{
		Session: window(10, now.Add(time.Hour)),
		Weekly:  window(100, now.Add(24*time.Hour)),
		Fable:   window(10, now.Add(24*time.Hour)),
	})
	if chosen := pick(t, s, "claude-fable-5", []*Auth{known, unknown}); chosen.ID != "acct-unknown" {
		t.Fatalf("chose %s, want acct-unknown when no polled credential is eligible", chosen.ID)
	}
}

func TestQuotaPickServesWhenPolicyExhausted(t *testing.T) {
	now := time.Now()
	s := newTestQuotaSelector()
	a := subscriptionAuth("acct-a")

	// The only account is ineligible on every axis the policy reads, but
	// upstream availability let it through — availability wins over
	// rejection.
	s.store.Set("acct-a", QuotaUsage{
		Session: window(99, now.Add(time.Hour)),
		Weekly:  window(100, now.Add(24*time.Hour)),
		Fable:   window(100, now.Add(24*time.Hour)),
	})

	if chosen := pick(t, s, "claude-sonnet-5", []*Auth{a}); chosen.ID != "acct-a" {
		t.Fatalf("chose %s, want acct-a (serve rather than reject)", chosen.ID)
	}
}

func TestQuotaPickDelegatesNonSubscriptionCandidates(t *testing.T) {
	s := newTestQuotaSelector()
	apiKey := &Auth{
		ID:         "acct-key",
		Provider:   "claude",
		Attributes: map[string]string{"auth_kind": "apikey", "api_key": "sk-test"},
	}
	if chosen := pick(t, s, "claude-sonnet-5", []*Auth{apiKey}); chosen.ID != "acct-key" {
		t.Fatalf("chose %s, want acct-key via fallback routing", chosen.ID)
	}
}
