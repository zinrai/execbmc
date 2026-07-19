package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bougou/go-ipmi/pkg/hal"
)

func TestExecChassisPowerStatusContract(t *testing.T) {
	cfg, _ := testConfig(t)
	boot := newBootdevStore(cfg.BootdevPath)

	on := &execChassis{commands: Commands{PowerStatus: "true"}, timeout: cfg.CommandTimeout(), boot: boot}
	state, err := on.PowerState(context.Background())
	if err != nil || !state {
		t.Fatalf("exit 0: state=%t err=%v, want on", state, err)
	}

	off := &execChassis{commands: Commands{PowerStatus: "false"}, timeout: cfg.CommandTimeout(), boot: boot}
	state, err = off.PowerState(context.Background())
	if err != nil || state {
		t.Fatalf("exit 1: state=%t err=%v, want off", state, err)
	}

	bad := &execChassis{commands: Commands{PowerStatus: "exit 2"}, timeout: cfg.CommandTimeout(), boot: boot}
	if _, err = bad.PowerState(context.Background()); err == nil {
		t.Fatal("exit 2 accepted as a power state")
	}
}

// recordingChassis builds an execChassis whose power_off and power_on append
// their name to a record file and whose power_status reports off, so the
// power-cycle fallbacks can be checked for the off-before-on ordering. reset
// and power_cycle are left unset so the fallback paths are exercised.
func recordingChassis(t *testing.T) (*execChassis, func() []string) {
	t.Helper()
	cfg, _ := testConfig(t)
	boot := newBootdevStore(cfg.BootdevPath)
	rec := filepath.Join(t.TempDir(), "ops")
	c := &execChassis{
		commands: Commands{
			PowerStatus: "false", // exit 1 = off, so waitPowerOff returns at once
			PowerOn:     "printf 'on\\n' >> " + rec,
			PowerOff:    "printf 'off\\n' >> " + rec,
		},
		timeout: cfg.CommandTimeout(),
		boot:    boot,
	}
	ops := func() []string {
		data, err := os.ReadFile(rec)
		if err != nil {
			t.Fatalf("read record: %v", err)
		}
		return strings.Fields(string(data))
	}
	return c, ops
}

// Without power_cycle, PowerCycle powers off, waits for off, then powers on.
func TestExecChassisPowerCycleFallback(t *testing.T) {
	c, ops := recordingChassis(t)
	if err := c.PowerCycle(context.Background()); err != nil {
		t.Fatalf("PowerCycle: %v", err)
	}
	if got := ops(); len(got) != 2 || got[0] != "off" || got[1] != "on" {
		t.Fatalf("op sequence = %v, want [off on]", got)
	}
}

// Without reset, ColdReset falls back to a power cycle.
func TestExecChassisColdResetFallsBackToCycle(t *testing.T) {
	c, ops := recordingChassis(t)
	if err := c.ColdReset(context.Background()); err != nil {
		t.Fatalf("ColdReset: %v", err)
	}
	if got := ops(); len(got) != 2 || got[0] != "off" || got[1] != "on" {
		t.Fatalf("op sequence = %v, want [off on]", got)
	}
}

// Without power_soft, a soft shutdown request is rejected.
func TestExecChassisWarmResetUnsupported(t *testing.T) {
	c, _ := recordingChassis(t)
	if err := c.WarmReset(context.Background()); !errors.Is(err, hal.ErrNotSupported) {
		t.Fatalf("WarmReset without power_soft = %v, want ErrNotSupported", err)
	}
}
