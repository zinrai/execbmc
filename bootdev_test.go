package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bougou/go-ipmi/pkg/types"
)

func TestBootdevFileRoundTrip(t *testing.T) {
	cfg, bootdevPath := testConfig(t)
	store := newBootdevStore(cfg.BootdevPath)

	flags := types.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		Persist:            false,
		BootDeviceSelector: types.BootDeviceSelectorForcePXE,
	}
	if err := store.Set(&flags); err != nil {
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

	got := store.Get()
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
	if err := store.Set(&flags); err == nil {
		t.Fatal("BIOS setup selector accepted")
	}
}
