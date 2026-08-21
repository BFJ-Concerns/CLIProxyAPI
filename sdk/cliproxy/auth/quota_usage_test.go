package auth

import (
	"testing"
	"time"
)

// The synthetic matrix's building blocks: fabricated window states with a
// fixed clock. Policy branches are proven over fabricated states because
// the interesting quota shapes cannot be manufactured live without burning
// real quota.

func defaultTestPolicy() QuotaPolicy {
	return QuotaPolicy{
		FableWeeklyRatio:   0.5,
		ReserveMargin:      10,
		SessionCeiling:     95,
		FableModelPrefixes: []string{"claude-fable"},
	}
}

func window(percent float64, resetsAt time.Time) *QuotaWindow {
	return &QuotaWindow{Percent: percent, ResetsAt: resetsAt}
}

func TestQuotaWindowReplenishesAfterReset(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		win         *QuotaWindow
		utilization float64
		headroom    float64
	}{
		{"nil window imposes nothing", nil, 0, 100},
		{"live window reads recorded percent", window(80, now.Add(time.Hour)), 80, 20},
		{"passed reset reads replenished", window(80, now.Add(-time.Minute)), 0, 100},
		{"reset exactly now reads replenished", window(80, now), 0, 100},
		{"zero reset time reads recorded percent", window(80, time.Time{}), 80, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.win.UtilizationAt(now); got != tc.utilization {
				t.Fatalf("UtilizationAt = %v, want %v", got, tc.utilization)
			}
			if got := tc.win.HeadroomAt(now); got != tc.headroom {
				t.Fatalf("HeadroomAt = %v, want %v", got, tc.headroom)
			}
		})
	}
}

// policyWith tweaks the default policy for parameter-edge cases; rows that
// need a non-default ratio or margin carry their own policy.
func policyWith(ratio, margin float64) QuotaPolicy {
	policy := defaultTestPolicy()
	policy.FableWeeklyRatio = ratio
	policy.ReserveMargin = margin
	return policy
}

func TestReservedWeeklyArithmetic(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	cases := []struct {
		name     string
		policy   QuotaPolicy
		usage    QuotaUsage
		reserved float64
	}{
		{
			name:     "half of fable headroom plus margin",
			policy:   defaultTestPolicy(),
			usage:    QuotaUsage{Fable: window(40, future)}, // 60 headroom
			reserved: 0.5*60 + 10,
		},
		{
			name:     "zero fable headroom reserves nothing",
			policy:   defaultTestPolicy(),
			usage:    QuotaUsage{Fable: window(100, future)},
			reserved: 0,
		},
		{
			name:     "missing fable window reserves nothing",
			policy:   defaultTestPolicy(),
			usage:    QuotaUsage{},
			reserved: 0,
		},
		{
			name:     "default maximum stays within the window",
			policy:   defaultTestPolicy(),
			usage:    QuotaUsage{Fable: window(0, future)}, // 100 headroom -> 60
			reserved: 60,
		},
		{
			name:     "oversized ratio clamps to the whole window",
			policy:   policyWith(1.5, 10),
			usage:    QuotaUsage{Fable: window(0, future)}, // 1.5*100+10 -> clamp 100
			reserved: 100,
		},
		{
			name:     "zero margin reserves the ratio share alone",
			policy:   policyWith(0.5, 0),
			usage:    QuotaUsage{Fable: window(40, future)},
			reserved: 30,
		},
		{
			name:     "expired fable reset counts as full headroom",
			policy:   defaultTestPolicy(),
			usage:    QuotaUsage{Fable: window(100, now.Add(-time.Minute))},
			reserved: 0.5*100 + 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.ReservedWeekly(tc.usage, now); got != tc.reserved {
				t.Fatalf("ReservedWeekly = %v, want %v", got, tc.reserved)
			}
		})
	}
}

func TestFreeWeeklyAtReserveBoundary(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	policy := defaultTestPolicy()

	// Fable headroom 40 -> reserve 0.5*40+10 = 30 points of weekly.
	fable := window(60, future)
	cases := []struct {
		name string
		// weekly utilisation percent
		weeklyUsed float64
		free       float64
	}{
		{"well above the reserve", 20, 80 - 30},
		{"exactly at the reserve boundary", 70, 0},
		{"inside the reserve", 80, -10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := QuotaUsage{Weekly: window(tc.weeklyUsed, future), Fable: fable}
			if got := policy.FreeWeekly(usage, now); got != tc.free {
				t.Fatalf("FreeWeekly = %v, want %v", got, tc.free)
			}
		})
	}
}

func TestEligibilityAcrossWindows(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	policy := defaultTestPolicy()
	cases := []struct {
		name         string
		usage        QuotaUsage
		wantFable    bool
		wantNonFable bool
	}{
		{
			name:         "all windows comfortable",
			usage:        QuotaUsage{Session: window(10, future), Weekly: window(10, future), Fable: window(10, future)},
			wantFable:    true,
			wantNonFable: true,
		},
		{
			name:         "weekly exhausted strands fable headroom (the headline case)",
			usage:        QuotaUsage{Session: window(10, future), Weekly: window(100, future), Fable: window(20, future)},
			wantFable:    false,
			wantNonFable: false,
		},
		{
			name:         "fable exhausted blocks only fable-class",
			usage:        QuotaUsage{Session: window(10, future), Weekly: window(50, future), Fable: window(100, future)},
			wantFable:    false,
			wantNonFable: true,
		},
		{
			name:         "session at the ceiling blocks new bindings",
			usage:        QuotaUsage{Session: window(95, future), Weekly: window(10, future), Fable: window(10, future)},
			wantFable:    false,
			wantNonFable: false,
		},
		{
			name:         "session just below the ceiling is open",
			usage:        QuotaUsage{Session: window(94.9, future), Weekly: window(10, future), Fable: window(10, future)},
			wantFable:    true,
			wantNonFable: true,
		},
		{
			name:         "expired weekly reset reads replenished",
			usage:        QuotaUsage{Session: window(10, future), Weekly: window(100, now.Add(-time.Minute)), Fable: window(20, future)},
			wantFable:    true,
			wantNonFable: true,
		},
		{
			name:         "missing windows impose no constraint",
			usage:        QuotaUsage{},
			wantFable:    true,
			wantNonFable: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.EligibleFable(tc.usage, now); got != tc.wantFable {
				t.Fatalf("EligibleFable = %v, want %v", got, tc.wantFable)
			}
			if got := policy.EligibleNonFable(tc.usage, now); got != tc.wantNonFable {
				t.Fatalf("EligibleNonFable = %v, want %v", got, tc.wantNonFable)
			}
		})
	}
}

func TestIsFableClassMatchesConfiguredPrefixes(t *testing.T) {
	policy := defaultTestPolicy()
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-fable-5", true},
		{"Claude-Fable-5", true},
		{"claude-fable-5(high)", true}, // thinking suffix is stripped by canonicalModelKey
		{"claude-opus-5", false},
		{"claude-sonnet-5", false},
		{"gpt-5.6-sol", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := policy.IsFableClass(tc.model); got != tc.want {
			t.Fatalf("IsFableClass(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestResetOrderingTreatsReplenishedAsLeastUrgent(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	soon := window(50, now.Add(time.Hour))
	later := window(50, now.Add(48*time.Hour))
	replenished := window(50, now.Add(-time.Minute))

	candidates := []quotaCandidate{
		{auth: &Auth{ID: "c-replenished"}, usage: QuotaUsage{Weekly: replenished}},
		{auth: &Auth{ID: "b-later"}, usage: QuotaUsage{Weekly: later}},
		{auth: &Auth{ID: "a-soon"}, usage: QuotaUsage{Weekly: soon}},
	}
	sortByWeeklyReset(candidates, now)
	got := []string{candidates[0].auth.ID, candidates[1].auth.ID, candidates[2].auth.ID}
	want := []string{"a-soon", "b-later", "c-replenished"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
