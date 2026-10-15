package main

import "testing"

func TestSystemGUIDDiffersForSharedPrefix(t *testing.T) {
	if systemGUID("node-rack1-01") == systemGUID("node-rack1-02") {
		t.Fatal("names sharing a prefix got the same GUID")
	}
}
