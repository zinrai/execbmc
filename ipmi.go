package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"

	"github.com/bougou/go-ipmi/pkg/bmc"
	"github.com/bougou/go-ipmi/pkg/clock"
	"github.com/bougou/go-ipmi/pkg/hal"
	"github.com/bougou/go-ipmi/pkg/server"
	"github.com/bougou/go-ipmi/pkg/transport/udp"
)

const (
	// Not slot 1: it is the anonymous user.
	configuredUserID = 2
	lanChannel       = 1
)

func systemGUID(name string) [16]byte {
	// Not random, so it survives a restart; not the name copied in, as names
	// sharing a prefix would share a GUID.
	sum := sha256.Sum256([]byte("execbmc:" + name))
	var guid [16]byte
	copy(guid[:], sum[:])
	return guid
}

func serveIPMI(ctx context.Context, cfg *Config, h hal.HAL) error {
	info := bmc.DeviceInfo{
		DeviceID:       1,
		DeviceRevision: 1,
		FirmwareMajor:  0,
		FirmwareMinor:  1,
		IPMIVersion:    0x20,
		ManufacturerID: 0x000000,
		ProductID:      0x0001,
	}
	b := bmc.New(info, systemGUID(cfg.Name), h, bmc.WithClock(clock.Real))

	user, err := b.Users.Add(configuredUserID, cfg.Username)
	if err != nil {
		return fmt.Errorf("add user: %w", err)
	}
	user.SetPassword([]byte(cfg.Password))
	user.Enabled = true
	user.ChannelAccess[lanChannel] = bmc.UserChannelAccess{
		MaxPrivilege: bmc.PrivilegeLevelAdministrator,
		Enabled:      true,
	}

	conn, err := udp.Listen(cfg.IPMI.Listen)
	if err != nil {
		return fmt.Errorf("listen udp %s: %w", cfg.IPMI.Listen, err)
	}
	defer conn.Close()

	srv := server.NewServer(b, conn)
	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	slog.Info("listening", "component", "ipmi", "addr", cfg.IPMI.Listen)
	if err := srv.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("ipmi serve: %w", err)
	}
	return nil
}
