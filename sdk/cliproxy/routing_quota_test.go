package cliproxy

import (
	"reflect"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// quotaAwareConfig selects the quota-aware strategy on a copy of cfg.
func quotaAwareConfig(cfg config.Config) *config.Config {
	cfg.Routing.Strategy = "quota-aware"
	return &cfg
}

func TestQuotaPolicyFromShippedDefaults(t *testing.T) {
	state := normalizedRoutingRuntimeState(quotaAwareConfig(config.Config{}))
	if state.strategy != "quota-aware" {
		t.Fatalf("strategy = %q, want quota-aware", state.strategy)
	}
	want := coreauth.QuotaPolicy{
		FableWeeklyRatio:   0.5,
		ReserveMargin:      10,
		SessionCeiling:     95,
		FableModelPrefixes: []string{"claude-fable"},
	}
	if got := quotaPolicyFromParams(state.quota); !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
}

func TestQuotaPolicyCarriesConfiguredValues(t *testing.T) {
	ratio, margin, ceiling := 0.25, 0.0, 90.0
	cfg := config.Config{}
	cfg.Routing.Quota.FableWeeklyRatio = &ratio
	cfg.Routing.Quota.ReserveMargin = &margin
	cfg.Routing.Quota.SessionCeiling = &ceiling
	cfg.Routing.Quota.FableModelPrefixes = []string{" Claude-Fable ", "", "CLAUDE-MYTHOS"}
	state := normalizedRoutingRuntimeState(quotaAwareConfig(cfg))
	want := coreauth.QuotaPolicy{
		FableWeeklyRatio:   ratio,
		ReserveMargin:      margin,
		SessionCeiling:     ceiling,
		FableModelPrefixes: []string{"claude-fable", "claude-mythos"},
	}
	if got := quotaPolicyFromParams(state.quota); !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
}

func TestQuotaRuntimeStateComparesByValue(t *testing.T) {
	first, second := 0.25, 0.25
	left := config.Config{}
	left.Routing.Quota.FableWeeklyRatio = &first
	left.Routing.Quota.FableModelPrefixes = []string{"claude-fable"}
	right := config.Config{}
	right.Routing.Quota.FableWeeklyRatio = &second
	right.Routing.Quota.FableModelPrefixes = []string{" CLAUDE-FABLE"}
	// Equal settings must yield equal state, or every reload would reinstall the selector.
	if normalizedRoutingRuntimeState(quotaAwareConfig(left)) != normalizedRoutingRuntimeState(quotaAwareConfig(right)) {
		t.Fatal("equal quota settings produced different routing state")
	}
	changed := 0.3
	right.Routing.Quota.FableWeeklyRatio = &changed
	if normalizedRoutingRuntimeState(quotaAwareConfig(left)) == normalizedRoutingRuntimeState(quotaAwareConfig(right)) {
		t.Fatal("a changed ratio left the routing state unchanged")
	}
}
