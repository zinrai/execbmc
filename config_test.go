package main

import (
	"path/filepath"
	"testing"
)

func testConfig(t *testing.T) (*config, string) {
	t.Helper()
	dir := t.TempDir()
	bootdevPath := filepath.Join(dir, "bootdev")
	return &config{
		Name:        "node1",
		Username:    "admin",
		Password:    "secret",
		IPMI:        ipmiConfig{Listen: ":0"},
		BootdevPath: bootdevPath,
		Commands: commandLines{
			PowerStatus: "true",
			PowerOn:     "true",
			PowerOff:    "true",
		},
	}, bootdevPath
}
