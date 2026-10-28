package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/bougou/go-ipmi/pkg/hal"
	"github.com/bougou/go-ipmi/pkg/types"
)

const maxLoggedOutput = 4 << 10

const outputDrainDelay = 500 * time.Millisecond

const powerOffPollInterval = 500 * time.Millisecond

func logOutput(b []byte) (string, bool) {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", false
	}
	if len(s) > maxLoggedOutput {
		s = s[:maxLoggedOutput] + "...(truncated)"
	}
	return s, true
}

func logExec(op, command string, code int, elapsed time.Duration, stdout, stderr []byte) {
	attrs := []any{
		"op", op,
		"cmd", command,
		"exit_code", code,
		"duration_ms", elapsed.Milliseconds(),
	}
	if out, ok := logOutput(stdout); ok {
		attrs = append(attrs, "stdout", out)
	}
	if out, ok := logOutput(stderr); ok {
		attrs = append(attrs, "stderr", out)
	}
	slog.Info("exec", attrs...)
}

type execHAL struct {
	chassis *execChassis
}

func newExecHAL(cfg *config, boot *bootdevStore) *execHAL {
	return &execHAL{
		chassis: &execChassis{
			commands: cfg.Commands,
			timeout:  cfg.commandTimeout(),
			boot:     boot,
		},
	}
}

func (h *execHAL) Chassis() hal.ChassisHAL { return h.chassis }
func (h *execHAL) Sensors() hal.SensorHAL  { return nil }
func (h *execHAL) Storage() hal.StorageHAL { return nil }
func (h *execHAL) Network() hal.NetworkHAL { return nil }
func (h *execHAL) GPIO() hal.GPIOHAL       { return nil }
func (h *execHAL) I2C() hal.I2CHAL         { return nil }
func (h *execHAL) Console() hal.ConsoleHAL { return nil }
func (h *execHAL) Close() error            { return nil }

type execChassis struct {
	commands commandLines
	timeout  time.Duration
	boot     *bootdevStore
}

func (c *execChassis) run(ctx context.Context, op string, command string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	start := time.Now()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Not unbounded: a machine detached with setsid still holds the inherited
	// stdout and stderr, and Wait would block until the machine stops.
	cmd.WaitDelay = outputDrainDelay
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			code = exitErr.ExitCode()
			err = nil
		case errors.Is(err, exec.ErrWaitDelay):
			code = cmd.ProcessState.ExitCode()
			err = nil
		default:
			code = -1
		}
	}

	logExec(op, command, code, time.Since(start), stdout.Bytes(), stderr.Bytes())

	if err != nil {
		return -1, fmt.Errorf("run %q: %w", command, err)
	}
	return code, nil
}

func (c *execChassis) runOK(ctx context.Context, op string, command string) error {
	code, err := c.run(ctx, op, command)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%q exited with code %d", command, code)
	}
	return nil
}

func (c *execChassis) PowerState(ctx context.Context) (bool, error) {
	code, err := c.run(ctx, "power_status", c.commands.PowerStatus)
	if err != nil {
		return false, err
	}
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("%q exited with code %d", c.commands.PowerStatus, code)
	}
}

func (c *execChassis) SetPower(ctx context.Context, on bool) error {
	if on {
		return c.runOK(ctx, "power_on", c.commands.PowerOn)
	}
	return c.runOK(ctx, "power_off", c.commands.PowerOff)
}

func (c *execChassis) PowerCycle(ctx context.Context) error {
	if c.commands.PowerCycle != "" {
		return c.runOK(ctx, "power_cycle", c.commands.PowerCycle)
	}
	if err := c.runOK(ctx, "power_off", c.commands.PowerOff); err != nil {
		return err
	}
	if err := c.waitPowerOff(ctx); err != nil {
		return err
	}
	return c.runOK(ctx, "power_on", c.commands.PowerOn)
}

func (c *execChassis) waitPowerOff(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for {
		on, err := c.PowerState(ctx)
		if err != nil {
			return err
		}
		if !on {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for power off")
		case <-time.After(powerOffPollInterval):
		}
	}
}

func (c *execChassis) ColdReset(ctx context.Context) error {
	if c.commands.Reset != "" {
		return c.runOK(ctx, "reset", c.commands.Reset)
	}
	return c.PowerCycle(ctx)
}

// Not a warm reboot: go-ipmi maps IPMI soft shutdown (Chassis Control 0x05)
// to WarmReset, so it runs power_soft.
func (c *execChassis) WarmReset(ctx context.Context) error {
	if c.commands.PowerSoft == "" {
		return hal.ErrNotSupported
	}
	return c.runOK(ctx, "power_soft", c.commands.PowerSoft)
}

// Not ErrNotSupported: go-ipmi reports Chassis Identify as supported in
// Get Chassis Status, so a client that then calls it would get an error.
func (c *execChassis) Identify(ctx context.Context, seconds uint8) error {
	slog.Info("identify", "seconds", seconds)
	return nil
}

func (c *execChassis) IntrusionState(ctx context.Context) (bool, error) {
	return false, hal.ErrNotSupported
}

func (c *execChassis) SetBootFlags(ctx context.Context, flags *types.BootOptionParam_BootFlags) error {
	f, err := c.boot.Set(flags)
	if err != nil {
		return err
	}
	slog.Info("boot_flags", "source", "ipmi", "target", f.Target, "persistent", f.Persistent)
	return nil
}

func (c *execChassis) GetBootFlags(ctx context.Context) (*types.BootOptionParam_BootFlags, error) {
	return c.boot.Get()
}

func (c *execChassis) SetBootInfoAcknowledge(ctx context.Context, ack *types.BootOptionParam_BootInfoAcknowledge) error {
	c.boot.SetAck(ack)
	return nil
}

func (c *execChassis) GetBootInfoAcknowledge(ctx context.Context) (*types.BootOptionParam_BootInfoAcknowledge, error) {
	return c.boot.GetAck(), nil
}
