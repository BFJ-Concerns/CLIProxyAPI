package config

import (
	"reflect"
	"testing"
)

func TestQuotaRoutingConfigDefaults(t *testing.T) {
	var cfg QuotaRoutingConfig
	if got := cfg.EffectiveFableWeeklyRatio(); got != 0.5 {
		t.Fatalf("default fable-weekly-ratio = %v, want 0.5", got)
	}
	if got := cfg.EffectiveReserveMargin(); got != 10 {
		t.Fatalf("default reserve-margin = %v, want 10", got)
	}
	if got := cfg.EffectiveSessionCeiling(); got != 95 {
		t.Fatalf("default session-ceiling = %v, want 95", got)
	}
	if got := cfg.EffectiveFableModelPrefixes(); !reflect.DeepEqual(got, []string{"claude-fable"}) {
		t.Fatalf("default fable-model-prefixes = %v, want [claude-fable]", got)
	}
}

func TestQuotaRoutingConfigHonoursConfiguredValues(t *testing.T) {
	ratio, margin, ceiling := 0.75, 0.0, 100.0
	cfg := QuotaRoutingConfig{
		FableWeeklyRatio:   &ratio,
		ReserveMargin:      &margin,
		SessionCeiling:     &ceiling,
		FableModelPrefixes: []string{"claude-mythos"},
	}
	if got := cfg.EffectiveFableWeeklyRatio(); got != ratio {
		t.Fatalf("fable-weekly-ratio = %v, want %v", got, ratio)
	}
	if got := cfg.EffectiveReserveMargin(); got != margin {
		t.Fatalf("reserve-margin = %v, want %v (zero is a valid setting)", got, margin)
	}
	if got := cfg.EffectiveSessionCeiling(); got != ceiling {
		t.Fatalf("session-ceiling = %v, want %v", got, ceiling)
	}
	if got := cfg.EffectiveFableModelPrefixes(); !reflect.DeepEqual(got, []string{"claude-mythos"}) {
		t.Fatalf("fable-model-prefixes = %v, want [claude-mythos]", got)
	}
}

func TestQuotaRoutingConfigOutOfRangeValuesFallBackToDefaults(t *testing.T) {
	negative, zero, over := -1.0, 0.0, 100.5
	cases := []struct {
		name string
		cfg  QuotaRoutingConfig
	}{
		{name: "negative values", cfg: QuotaRoutingConfig{FableWeeklyRatio: &negative, ReserveMargin: &negative, SessionCeiling: &negative}},
		{name: "zero ceiling", cfg: QuotaRoutingConfig{SessionCeiling: &zero}},
		{name: "ceiling above 100", cfg: QuotaRoutingConfig{SessionCeiling: &over}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cfg.FableWeeklyRatio != nil && tc.cfg.EffectiveFableWeeklyRatio() != 0.5 {
				t.Fatalf("fable-weekly-ratio = %v, want default 0.5", tc.cfg.EffectiveFableWeeklyRatio())
			}
			if tc.cfg.ReserveMargin != nil && tc.cfg.EffectiveReserveMargin() != 10 {
				t.Fatalf("reserve-margin = %v, want default 10", tc.cfg.EffectiveReserveMargin())
			}
			if got := tc.cfg.EffectiveSessionCeiling(); got != 95 {
				t.Fatalf("session-ceiling = %v, want default 95", got)
			}
		})
	}
}
