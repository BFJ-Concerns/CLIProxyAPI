// Fork addition (usage spool): the YAML surface and defaults for the
// durable usage and quota-reading spool.
package config

// UsageSpoolConfig configures the durable spool of per-request usage
// records and quota readings read through the management usage-spool
// endpoints. Usage records reach the spool only while
// usage-statistics-enabled is true. Changes apply on restart.
type UsageSpoolConfig struct {
	// Enabled turns the spool on.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Dir is the spool directory. Relative paths resolve against the
	// config file's directory. Default: "usage-spool".
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// RetentionDays drops records older than this many days whether or
	// not they were acknowledged. Default: 14.
	RetentionDays int `yaml:"retention-days,omitempty" json:"retention-days,omitempty"`
}

// EffectiveDir returns the configured directory or its default.
func (c UsageSpoolConfig) EffectiveDir() string {
	if c.Dir != "" {
		return c.Dir
	}
	return "usage-spool"
}

// EffectiveRetentionDays returns the configured retention or its default.
func (c UsageSpoolConfig) EffectiveRetentionDays() int {
	if c.RetentionDays > 0 {
		return c.RetentionDays
	}
	return 14
}
