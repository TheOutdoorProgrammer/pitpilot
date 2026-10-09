package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/api"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/telemetry"
)

var version = "dev"
var commit = "unknown"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "discard-legacy-signals" {
		if err := discardLegacySignals(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "pitpilot legacy signal discard:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "recover-receiver" {
		if err := recoverReceiver(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "pitpilot receiver recovery:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "convert-summary-metrics" {
		if err := convertSummaries(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "pitpilot summary conversion:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		if err := backup(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "pitpilot backup:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate-lubelogger" {
		if err := migrate(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "pitpilot migration:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "help") {
		fmt.Println("PitPilot vehicle journal.\n\nCommands:\n  version\n  migrate-lubelogger --help\n  convert-summary-metrics --help\n  recover-receiver --help\n  discard-legacy-signals --help\n  backup --help\n\nWith no command, starts the API server configured by PITPILOT_* environment variables.")
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Printf("pitpilot %s (%s)\n", version, commit)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pitpilot:", err)
		os.Exit(1)
	}
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	token := strings.TrimSpace(os.Getenv("PITPILOT_API_TOKEN"))
	if filename := os.Getenv("PITPILOT_API_TOKEN_FILE"); filename != "" {
		data, err := os.ReadFile(filename)
		if err != nil {
			return errors.New("cannot read API token file")
		}
		token = strings.TrimSpace(string(data))
	}
	if len(token) < 32 {
		return errors.New("configure PITPILOT_API_TOKEN_FILE or PITPILOT_API_TOKEN with at least 32 random characters")
	}
	logger, shutdown, err := telemetry.Start(ctx, version)
	if err != nil {
		return errors.New("telemetry initialization failed")
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if shutdown(stop) != nil {
			fmt.Fprintln(os.Stderr, "telemetry shutdown failed")
		}
	}()
	store, err := garage.Open(env("PITPILOT_DB", "data/pitpilot.db"))
	if err != nil {
		return errors.New("database initialization failed")
	}
	defer store.Close()
	metricsToken := ""
	if filename := os.Getenv("PITPILOT_METRICS_TOKEN_FILE"); filename != "" {
		data, readErr := os.ReadFile(filename)
		if readErr != nil {
			return errors.New("cannot read metrics token file")
		}
		metricsToken = strings.TrimSpace(string(data))
		if len(metricsToken) < 32 {
			return errors.New("metrics token file must contain at least 32 random characters")
		}
	}
	smartcarService, err := configuredSmartcar(store, logger)
	if err != nil {
		return err
	}
	workerCtx, stopWorkers := context.WithCancel(ctx)
	workersDone := make(chan struct{})
	if smartcarService != nil {
		go func() { defer close(workersDone); smartcarService.Run(workerCtx) }()
	} else {
		close(workersDone)
	}
	defer func() { stopWorkers(); <-workersDone }()
	handler, err := api.NewWithOptions(store, token, logger, api.Options{MetricsToken: metricsToken, Smartcar: smartcarService})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: env("PITPILOT_ADDR", ":8080"), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	failures := make(chan error, 1)
	go func() { logger.Info("server started", "version", version); failures <- server.ListenAndServe() }()
	select {
	case err = <-failures:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server failed")
		}
	case <-ctx.Done():
	}
	stop, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	if err = server.Shutdown(stop); err != nil {
		_ = server.Close()
		return errors.New("HTTP shutdown timed out")
	}
	logger.Info("server stopped")
	return nil
}
