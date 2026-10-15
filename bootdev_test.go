package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bougou/go-ipmi/pkg/types"
)

func TestBootdevSetWritesFileAndGetReadsItBack(t *testing.T) {
	cfg, bootdevPath := testConfig(t)
	store := newBootdevStore(cfg.BootdevPath)

	flags := types.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		Persist:            false,
		BootDeviceSelector: types.BootDeviceSelectorForcePXE,
	}
	if _, err := store.Set(&flags); err != nil {
		t.Fatalf("Set: %v", err)
	}

	data, err := os.ReadFile(bootdevPath)
	if err != nil {
		t.Fatalf("read bootdev file: %v", err)
	}
	var f bootdevFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse bootdev file: %v", err)
	}
	if f.Target != "pxe" || f.Persistent {
		t.Fatalf("bootdev file = %+v, want target pxe persistent false", f)
	}

	got, err := store.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BootDeviceSelector != types.BootDeviceSelectorForcePXE || !got.BootFlagsValid {
		t.Fatalf("Get = %+v, want ForcePXE valid", got)
	}
}

func TestBootdevRejectsUnsupportedSelector(t *testing.T) {
	cfg, _ := testConfig(t)
	store := newBootdevStore(cfg.BootdevPath)

	flags := types.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		BootDeviceSelector: types.BootDeviceSelectorForceBIOSSetup,
	}
	if _, err := store.Set(&flags); err == nil {
		t.Fatal("BIOS setup selector accepted")
	}
}

// A reset of the file by its reader disables the override, and a store
// started on an existing file reports the override it holds.
func TestBootdevReadsFileBack(t *testing.T) {
	cfg, bootdevPath := testConfig(t)
	store := newBootdevStore(cfg.BootdevPath)
	flags := types.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		BootDeviceSelector: types.BootDeviceSelectorForcePXE,
	}
	if _, err := store.Set(&flags); err != nil {
		t.Fatalf("Set: %v", err)
	}

	restarted := newBootdevStore(cfg.BootdevPath)
	got, err := restarted.Get()
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if !got.BootFlagsValid || got.BootDeviceSelector != types.BootDeviceSelectorForcePXE {
		t.Fatalf("Get after restart = %+v, want ForcePXE valid", got)
	}

	if err := os.WriteFile(bootdevPath, []byte(`{"target": "none", "persistent": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get()
	if err != nil {
		t.Fatalf("Get after reset: %v", err)
	}
	if got.BootFlagsValid {
		t.Fatalf("Get after reset = %+v, want override disabled", got)
	}
}
