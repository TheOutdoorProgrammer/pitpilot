package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/telemetry"
)

var version = "dev"
var commit = "unknown"
var updatePublicKey = ""

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "picollector operation failed:", err)
		os.Exit(1)
	}
}
func execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: picollector run|enroll|update|drain|version")
	}
	if args[0] == "version" {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		}{version, commit})
	}
	f := flag.NewFlagSet("picollector", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	config := f.String("config", "/etc/pitpilot/collector.json", "configuration file")
	token := f.String("token-file", "", "one-use enrollment token file")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 {
		return errors.New("invalid command flags")
	}
	c, err := picollector.ReadConfig(*config)
	if err != nil {
		return err
	}
	logger, shutdown, err := telemetry.StartToNamed(ctx, "picollector", version, os.Stderr)
	if err != nil {
		return errors.New("telemetry initialization failed")
	}
	slog.SetDefault(logger)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	switch args[0] {
	case "run":
		return picollector.Run(ctx, c, version, nil)
	case "drain":
		return picollector.Drain(ctx, c)
	case "enroll":
		if *token == "" {
			return errors.New("enrollment requires --token-file")
		}
		return picollector.Enroll(ctx, c, *token)
	case "update":
		if os.Geteuid() != 0 {
			return errors.New("updater requires its dedicated root systemd service")
		}
		u, err := picollector.NewUpdater(updatePublicKey, version, c)
		if err != nil {
			return err
		}
		return diag.Operation(ctx, "collector.update", func(ctx context.Context) error { return u.Check(ctx, c) })
	default:
		return errors.New("unknown command")
	}
}
