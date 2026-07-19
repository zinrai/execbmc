// execbmc serves IPMI over LAN and a Redfish subset for one machine, and
// translates chassis operations into configured commands. It is the BMC for
// a machine that does not have one: a QEMU guest, a container, a lab box.
//
// One process is one BMC. It runs in the foreground and never becomes the
// parent of the machine it manages.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// version, commit and date are set by goreleaser via -ldflags -X.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to the JSON config file (required)")
	versionFlag := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("execbmc version %s (commit: %s, built: %s)\n", version, commit, date)
		return nil
	}

	if *configPath == "" {
		flag.Usage()
		return fmt.Errorf("-config is required")
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	boot := newBootdevStore(cfg.BootdevPath)
	h := newExecHAL(cfg, boot)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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

	// The first server error stops the process; on signal, both return nil.
	for i := 0; i < servers; i++ {
		if err := <-errCh; err != nil {
			cancel()
			return err
		}
	}
	return nil
}
