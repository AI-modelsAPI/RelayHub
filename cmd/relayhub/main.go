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
	"strings"
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
		httpProxyAddr  string
		socks5Addr     string
		gatewayAddr    string
	)
	managementDefault := config.DefaultManagementAddr
	if value, present := os.LookupEnv("RELAYHUB_MANAGEMENT_ADDR"); present {
		managementDefault = value
	}
	fs.StringVar(&managementAddr, "management-addr", managementDefault, "loopback management address (overrides RELAYHUB_MANAGEMENT_ADDR)")
	// The three data-plane listeners accept non-loopback addresses so a
	// container can publish them (AUDIT RH-04: every listener was pinned to
	// the container's own loopback, making `-p 8789:8789` unreachable). The
	// management plane stays loopback-only by design.
	fs.StringVar(&httpProxyAddr, "http-proxy-addr", envOr("RELAYHUB_HTTP_PROXY_ADDR", config.DefaultHTTPProxyAddr), "HTTP/HTTPS CONNECT proxy listen address (overrides RELAYHUB_HTTP_PROXY_ADDR)")
	fs.StringVar(&socks5Addr, "socks5-addr", envOr("RELAYHUB_SOCKS5_ADDR", config.DefaultSOCKS5Addr), "SOCKS5 proxy listen address (overrides RELAYHUB_SOCKS5_ADDR)")
	fs.StringVar(&gatewayAddr, "gateway-addr", envOr("RELAYHUB_GATEWAY_ADDR", config.DefaultGatewayAddr), "AI gateway listen address (overrides RELAYHUB_GATEWAY_ADDR)")
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

	if fs.NArg() == 1 && fs.Arg(0) == "mcp" {
		return runMCPStdio(managementAddr)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if err := validateManagementAddress(managementAddr); err != nil {
		return err
	}
	for _, l := range []struct{ name, addr string }{
		{"http proxy", httpProxyAddr}, {"socks5 proxy", socks5Addr}, {"gateway", gatewayAddr},
	} {
		if err := validateListenAddress(l.name, l.addr); err != nil {
			return err
		}
	}
	// Optional <data-dir>/config.json plus RELAYHUB_* environment overrides
	// supply non-secret settings (egress proxy, notification sinks). Listen
	// addresses keep coming from flags/env as before.
	fileCfg, err := config.Load(dataDir)
	if err != nil {
		return err
	}
	config.ApplyEnv(&fileCfg)
	a, err := app.New(app.Config{
		DataDir:        dataDir,
		HTTPProxyAddr:  httpProxyAddr,
		SOCKS5Addr:     socks5Addr,
		GatewayAddr:    gatewayAddr,
		ManagementAddr: managementAddr,
		WireFullStack:  fullStack,
		EgressProxyURL: fileCfg.EgressProxyURL,
		// The target policies were parsed from config.json / env but never
		// handed to the app, so "local_only" silently stayed "open" (AUDIT
		// 2026-09-24 F2).
		ManagementToken:       fileCfg.ManagementToken,
		ProxyUsername:         fileCfg.ProxyUsername,
		ProxyPassword:         fileCfg.ProxyPassword,
		HTTPProxyTargetPolicy: fileCfg.HTTPProxyTargetPolicy,
		SOCKS5TargetPolicy:    fileCfg.SOCKS5TargetPolicy,
		Notify: app.NotifyConfig{
			WebhookURL:       fileCfg.Notify.WebhookURL,
			BarkURL:          fileCfg.Notify.BarkURL,
			TelegramBotToken: fileCfg.Notify.TelegramBotToken,
			TelegramChatID:   fileCfg.Notify.TelegramChatID,
			QuotaLowUSD:      fileCfg.Notify.QuotaLowUSD,
		},
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

// envOr returns the environment value for key, or fallback when the variable
// is unset or empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// defaultDataDir returns a per-user default state directory. It never fails; if
// the user config directory cannot be determined it falls back to a local path.
func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "relayhub")
	}
	return filepath.Join(".", "relayhub-data")
}
