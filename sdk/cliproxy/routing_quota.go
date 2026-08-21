// Fork addition (quota-aware routing): the bridge from configuration to
// the quota-aware selector's policy. Parameters are carried inside
// routingRuntimeState as comparable scalars (the prefix list canonicalised
// to one string) so the reload path's state comparison keeps working.
package cliproxy

import (
	"strings"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// quotaRuntimeParams is the normalised, comparable form of
// config.QuotaRoutingConfig.
type quotaRuntimeParams struct {
	fableWeeklyRatio   float64
	reserveMargin      float64
	sessionCeiling     float64
	fableModelPrefixes string
}

// quotaRuntimeParamsFromConfig normalises the quota routing configuration:
// defaults applied, prefixes lower-cased, trimmed, and comma-joined.
func quotaRuntimeParamsFromConfig(cfg *config.Config) quotaRuntimeParams {
	quota := cfg.Routing.Quota
	prefixes := make([]string, 0, len(quota.EffectiveFableModelPrefixes()))
	for _, prefix := range quota.EffectiveFableModelPrefixes() {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	return quotaRuntimeParams{
		fableWeeklyRatio:   quota.EffectiveFableWeeklyRatio(),
		reserveMargin:      quota.EffectiveReserveMargin(),
		sessionCeiling:     quota.EffectiveSessionCeiling(),
		fableModelPrefixes: strings.Join(prefixes, ","),
	}
}

// quotaPolicyFromParams expands the comparable form back into the
// selector's policy.
func quotaPolicyFromParams(params quotaRuntimeParams) coreauth.QuotaPolicy {
	var prefixes []string
	for _, prefix := range strings.Split(params.fableModelPrefixes, ",") {
		if prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	return coreauth.QuotaPolicy{
		FableWeeklyRatio:   params.fableWeeklyRatio,
		ReserveMargin:      params.reserveMargin,
		SessionCeiling:     params.sessionCeiling,
		FableModelPrefixes: prefixes,
	}
}
