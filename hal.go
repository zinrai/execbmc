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

// maxLoggedOutput caps how much of a command's stdout or stderr is copied
// into a log line, so a runaway command cannot flood the log.
const maxLoggedOutput = 4 << 10 // 4 KiB

// logOutput trims a captured stream for logging and reports whether anything
// is left. Empty or whitespace-only output is dropped so a quiet command
// (the common case for a status poll) does not add attributes.
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

// execHAL is a hal.HAL whose only subsystem is a chassis backed by
// configured commands. Every other subsystem is absent.
type execHAL struct {
	chassis *execChassis
}

func newExecHAL(cfg *Config, boot *bootdevStore) *execHAL {
	return &execHAL{
		chassis: &execChassis{
			commands: cfg.Commands,
			timeout:  cfg.CommandTimeout(),
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
func (h *execHAL) Close() error            { return nil }

// execChassis implements hal.ChassisHAL by running configured commands.
type execChassis struct {
	commands Commands
	timeout  time.Duration
	boot     *bootdevStore
}

// run executes command with /bin/sh -c under the configured timeout,
// logs the execution, and returns its exit code.
func (c *execChassis) run(ctx context.Context, op string, command string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	start := time.Now()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
			err = nil
		} else {
			code = -1
		}
	}

	// The exit code carries the result; stdout and stderr are logged purely
	// for observability, so they are included only when the command wrote to
	// them.
	attrs := []any{
		"op", op,
		"cmd", command,
		"exit_code", code,
		"duration_ms", time.Since(start).Milliseconds(),
	}
	if out, ok := logOutput(stdout.Bytes()); ok {
		attrs = append(attrs, "stdout", out)
	}
	if out, ok := logOutput(stderr.Bytes()); ok {
		attrs = append(attrs, "stderr", out)
	}
	slog.Info("exec", attrs...)

	if err != nil {
		return -1, fmt.Errorf("run %q: %w", command, err)
	}
	return code, nil
}

// runOK executes command and requires exit code 0.
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

// PowerState runs power_status: exit 0 means on, exit 1 means off.
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

// SetPower runs power_on or power_off.
func (c *execChassis) SetPower(ctx context.Context, on bool) error {
	if on {
		return c.runOK(ctx, "power_on", c.commands.PowerOn)
	}
	return c.runOK(ctx, "power_off", c.commands.PowerOff)
}

// PowerCycle runs power_cycle when configured, otherwise powers off, waits
// for power_status to report off, and powers on.
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

// waitPowerOff polls power_status until it reports off.
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
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// ColdReset runs reset when configured, otherwise performs a power cycle.
func (c *execChassis) ColdReset(ctx context.Context) error {
	if c.commands.Reset != "" {
		return c.runOK(ctx, "reset", c.commands.Reset)
	}
	return c.PowerCycle(ctx)
}

// WarmReset runs power_soft. The chassis handler maps IPMI soft shutdown
// (Chassis Control 0x05) to WarmReset.
func (c *execChassis) WarmReset(ctx context.Context) error {
	if c.commands.PowerSoft == "" {
		return hal.ErrNotSupported
	}
	return c.runOK(ctx, "power_soft", c.commands.PowerSoft)
}

// Identify logs the request. There is no identification LED to pulse.
func (c *execChassis) Identify(ctx context.Context, seconds uint8) error {
	slog.Info("identify", "seconds", seconds)
	return nil
}

// IntrusionState is not supported.
func (c *execChassis) IntrusionState(ctx context.Context) (bool, error) {
	return false, hal.ErrNotSupported
}

// SetBootFlags stores the flags and writes the bootdev file.
func (c *execChassis) SetBootFlags(ctx context.Context, flags *types.BootOptionParam_BootFlags) error {
	if err := c.boot.Set(flags); err != nil {
		return err
	}
	target, persistent := c.boot.Target()
	slog.Info("boot_flags", "source", "ipmi", "target", target, "persistent", persistent)
	return nil
}

// GetBootFlags returns the stored flags.
func (c *execChassis) GetBootFlags(ctx context.Context) (*types.BootOptionParam_BootFlags, error) {
	return c.boot.Get(), nil
}

// SetBootInfoAcknowledge stores the acknowledge data.
func (c *execChassis) SetBootInfoAcknowledge(ctx context.Context, ack *types.BootOptionParam_BootInfoAcknowledge) error {
	c.boot.SetAck(ack)
	return nil
}

// GetBootInfoAcknowledge returns the stored acknowledge data.
func (c *execChassis) GetBootInfoAcknowledge(ctx context.Context) (*types.BootOptionParam_BootInfoAcknowledge, error) {
	return c.boot.GetAck(), nil
}
