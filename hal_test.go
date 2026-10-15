package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// A command that leaves a background process holding stdout returns once
// the shell exits, not when the background process does.
func TestExecChassisReturnsWhenShellExits(t *testing.T) {
	cfg, _ := testConfig(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() { killPIDFile(t, pidFile) })
	c := &execChassis{
		commands: Commands{PowerOn: "sleep 10 & echo $! > " + pidFile},
		timeout:  cfg.CommandTimeout(),
		boot:     newBootdevStore(cfg.BootdevPath),
	}
	start := time.Now()
	if err := c.SetPower(context.Background(), true); err != nil {
		t.Fatalf("SetPower: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("SetPower returned after %v, want shortly after the shell exits", elapsed)
	}
}

func killPIDFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read pid file: %v", err)
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Errorf("parse pid file: %v", err)
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}

// recordingChassis builds an execChassis whose power_off and power_on append
// their name to a record file, and whose power_status reports off.
func recordingChassis(t *testing.T) (*execChassis, func() []string) {
	t.Helper()
	cfg, _ := testConfig(t)
	boot := newBootdevStore(cfg.BootdevPath)
	rec := filepath.Join(t.TempDir(), "ops")
	c := &execChassis{
		commands: Commands{
			PowerStatus: "false",
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
