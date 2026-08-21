// Fork addition (quota-aware routing): the YAML surface and defaults for
// the "quota-aware" routing strategy.
package config

// QuotaRoutingConfig configures the quota-aware routing strategy
// (routing.strategy: "quota-aware"). All fields are optional; absent
// values take the defaults the Effective methods return.
type QuotaRoutingConfig struct {
	// FableWeeklyRatio is the weekly-window points assumed consumed per
	// point of Fable-window headroom served. Default: 0.5 (the Fable cap
	// assumed at half the weekly cap in the same cost units).
	FableWeeklyRatio *float64 `yaml:"fable-weekly-ratio,omitempty" json:"fable-weekly-ratio,omitempty"`

	// ReserveMargin is added to every non-zero Fable reserve, in
	// percentage points of the weekly window. Default: 10.
	ReserveMargin *float64 `yaml:"reserve-margin,omitempty" json:"reserve-margin,omitempty"`

	// SessionCeiling is the 5-hour-window utilisation percent at or above
	// which an account is skipped for new session bindings. Default: 95.
	SessionCeiling *float64 `yaml:"session-ceiling,omitempty" json:"session-ceiling,omitempty"`

	// FableModelPrefixes classifies a request as Fable-class when the
	// requested model matches any prefix (case-insensitive). Default:
	// ["claude-fable"].
	FableModelPrefixes []string `yaml:"fable-model-prefixes,omitempty" json:"fable-model-prefixes,omitempty"`
}

// EffectiveFableWeeklyRatio returns the configured ratio or its default.
func (c QuotaRoutingConfig) EffectiveFableWeeklyRatio() float64 {
	if c.FableWeeklyRatio != nil && *c.FableWeeklyRatio >= 0 {
		return *c.FableWeeklyRatio
	}
	return 0.5
}

// EffectiveReserveMargin returns the configured margin or its default.
func (c QuotaRoutingConfig) EffectiveReserveMargin() float64 {
	if c.ReserveMargin != nil && *c.ReserveMargin >= 0 {
		return *c.ReserveMargin
	}
	return 10
}

// EffectiveSessionCeiling returns the configured ceiling or its default.
func (c QuotaRoutingConfig) EffectiveSessionCeiling() float64 {
	if c.SessionCeiling != nil && *c.SessionCeiling > 0 && *c.SessionCeiling <= 100 {
		return *c.SessionCeiling
	}
	return 95
}

// EffectiveFableModelPrefixes returns the configured prefix list or its
// default.
func (c QuotaRoutingConfig) EffectiveFableModelPrefixes() []string {
	if len(c.FableModelPrefixes) > 0 {
		return c.FableModelPrefixes
	}
	return []string{"claude-fable"}
}
