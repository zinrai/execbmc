package main

import (
	"path/filepath"
	"testing"
)

// testConfig returns a minimal valid config and its bootdev path, backed by a
// temp directory. Commands default to true/true/true so power operations
// succeed without a real machine.
func testConfig(t *testing.T) (*Config, string) {
	t.Helper()
	dir := t.TempDir()
	bootdevPath := filepath.Join(dir, "bootdev")
	return &Config{
		Name:        "node1",
		Username:    "admin",
		Password:    "secret",
		IPMI:        IPMIConfig{Listen: ":0"},
		BootdevPath: bootdevPath,
		Commands: Commands{
			PowerStatus: "true",
			PowerOn:     "true",
			PowerOff:    "true",
		},
	}, bootdevPath
}

// The only cross-field invariant worth asserting is that at least one
// listener is configured. The remaining checks are single-field presence
// guards that a test would only restate.
func TestConfigValidateRequiresListener(t *testing.T) {
	cfg, _ := testConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	noListener := *cfg
	noListener.IPMI.Listen = ""
	noListener.Redfish.Listen = ""
	if err := noListener.Validate(); err == nil {
		t.Fatal("config with neither ipmi.listen nor redfish.listen accepted")
	}
}
