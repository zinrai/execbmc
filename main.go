// execbmc serves IPMI over LAN and a Redfish subset for one machine, and
// translates chassis operations into configured commands.
//
// One process is one BMC. It runs in the foreground and never becomes the
// parent of the machine it manages.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.Func("config", "path to the JSON config file (required)", func(v string) error {
		if configPath != "" {
			return errors.New("-config given more than once")
		}
		configPath = v
		return nil
	})
	versionFlag := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("execbmc version %s (commit: %s, built: %s)\n", version, commit, date)
		return nil
	}

	if configPath == "" {
		flag.Usage()
		return fmt.Errorf("-config is required")
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return serve(ctx, cfg)
}

func serve(ctx context.Context, cfg *config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	boot := newBootdevStore(cfg.BootdevPath)
	h := newExecHAL(cfg, boot)

	errCh := make(chan error, 2)
	servers := 0

	if cfg.IPMI.Listen != "" {
		servers++
		go func() {
			errCh <- serveIPMI(ctx, cfg, h)
		}()
	}
	if cfg.Redfish.Listen != "" {
		servers++
		go func() {
			errCh <- serveRedfish(ctx, cfg, h.Chassis(), boot)
		}()
	}

	for i := 0; i < servers; i++ {
		if err := <-errCh; err != nil {
			return err
		}
	}
	return nil
}
