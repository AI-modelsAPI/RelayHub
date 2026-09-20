// Command relayhub is the RelayHub Core executable. In this bootstrap stage it
// starts the Core in the foreground, reports version/build information, and
// shuts down cleanly on SIGINT/SIGTERM. Networking, storage, and the
// management API are added in later tasks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"relayhub/internal/app"
	"relayhub/internal/buildinfo"
	"relayhub/internal/config"
)

// shutdownTimeout bounds how long a graceful shutdown may take before the
// process exits regardless.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		log.SetFlags(0)
		log.Fatalf("relayhub: %v", err)
	}
}

// run parses flags and drives the app lifecycle. It is separated from main so
// its error handling stays testable and free of os.Exit.
func run(args []string) error {
	fs := flag.NewFlagSet("relayhub", flag.ContinueOnError)
	var (
		showVersion    bool
		dataDir        string
		fullStack      bool
		managementAddr string
	)
	managementDefault := config.DefaultManagementAddr
	if value, present := os.LookupEnv("RELAYHUB_MANAGEMENT_ADDR"); present {
		managementDefault = value
	}
	fs.StringVar(&managementAddr, "management-addr", managementDefault, "loopback management address (overrides RELAYHUB_MANAGEMENT_ADDR)")
	fs.BoolVar(&showVersion, "version", false, "print version information and exit")
	fs.StringVar(&dataDir, "data-dir", defaultDataDir(), "directory for RelayHub state")
	fs.BoolVar(&fullStack, "full-stack", false, "start full stack (proxies, gateway, management API)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if showVersion {
		fmt.Println(buildinfo.Get().String())
		return nil
	}

	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if err := validateManagementAddress(managementAddr); err != nil {
		return err
	}
	a, err := app.New(app.Config{
		DataDir:        dataDir,
		ManagementAddr: managementAddr,
		WireFullStack:  fullStack,
	})
	if err != nil {
		return err
	}

	// Cancel the run context on the first interrupt/terminate signal.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := a.Start(ctx); err != nil {
		return err
	}
	log.Printf("relayhub started: %s (data-dir %s)", a.Info(), dataDir)

	// Block until a shutdown signal arrives.
	<-ctx.Done()
	log.Printf("relayhub: shutdown signal received, stopping")

	// Use a fresh, bounded context so shutdown is not already-cancelled by the
	// signal that triggered it.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Printf("relayhub: stopped cleanly")
	return nil
}

// defaultDataDir returns a per-user default state directory. It never fails; if
// the user config directory cannot be determined it falls back to a local path.
func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "relayhub")
	}
	return filepath.Join(".", "relayhub-data")
}
