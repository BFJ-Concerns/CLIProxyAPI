package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The fork's config sections must survive the v8 layout: migration moves them
// to their v8 paths instead of commenting them out, and a v8 file sets them.
func TestForkSectionsMigrateToV8Layout(t *testing.T) {
	raw := []byte(`port: 8317
usage-statistics-enabled: true
usage-spool:
  enabled: true
  dir: spool
  retention-days: 7
routing:
  strategy: quota-aware
  quota:
    fable-weekly-ratio: 0.25
    reserve-margin: 5
`)
	before, err := ParseConfigBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	migrated, changed, err := NormalizeConfigLayout(raw, true)
	if err != nil || !changed {
		t.Fatalf("migrate legacy file: changed=%v error=%v", changed, err)
	}
	if strings.Contains(string(migrated), "# usage-spool") || strings.Contains(string(migrated), "# routing.quota") {
		t.Fatalf("fork section was commented out by migration:\n%s", migrated)
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(migrated, &doc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"observability.usage.spool.enabled", "routing.quota.reserve-margin"} {
		if yamlPath(doc.Content[0], path) == nil {
			t.Fatalf("%s missing after migration:\n%s", path, migrated)
		}
	}
	if err = ValidateV8Config(migrated); err != nil {
		t.Fatalf("migrated file is invalid: %v", err)
	}
	after, err := ParseConfigBytes(migrated)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.UsageSpool, after.UsageSpool) || !reflect.DeepEqual(before.Routing.Quota, after.Routing.Quota) {
		t.Fatalf("migration changed fork settings: before=%+v %+v after=%+v %+v", before.UsageSpool, before.Routing.Quota, after.UsageSpool, after.Routing.Quota)
	}
}

func TestForkSectionsLoadFromV8Layout(t *testing.T) {
	raw := []byte(`config-version: 8
routing:
  strategy: quota-aware
  quota:
    session-ceiling: 90
observability:
  usage:
    usage-statistics-enabled: true
    spool:
      enabled: true
      retention-days: 3
`)
	if err := ValidateV8Config(raw); err != nil {
		t.Fatalf("v8 file is invalid: %v", err)
	}
	cfg, err := ParseConfigBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsageSpool.Enabled || cfg.UsageSpool.RetentionDays != 3 {
		t.Fatalf("usage spool not loaded from v8 path: %+v", cfg.UsageSpool)
	}
	if cfg.Routing.Strategy != "quota-aware" || cfg.Routing.Quota.SessionCeiling == nil || *cfg.Routing.Quota.SessionCeiling != 90 {
		t.Fatalf("quota routing not loaded from v8 layout: %+v", cfg.Routing)
	}
}
