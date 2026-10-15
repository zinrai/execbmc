package main

import (
	"path/filepath"
	"testing"
)

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
