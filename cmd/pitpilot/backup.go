package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

func backup(args []string, output io.Writer) (runErr error) {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(output)
	source := flags.String("database", "", "existing SQLite database; no migrations are run")
	destination := flags.String("output", "", "new private backup file in an existing directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *source == "" || *destination == "" {
		return errors.New("provide --database and --output; use --help")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	logger, shutdown, err := telemetry.Start(ctx, version)
	if err != nil {
		return errors.New("backup telemetry initialization failed")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if shutdown(shutdownCtx) != nil {
			runErr = errors.Join(runErr, errors.New("backup telemetry shutdown failed"))
		}
	}()
	ctx, span := otel.Tracer("pitpilot/backup").Start(ctx, "backup.cli")
	defer span.End()
	if err = garage.Backup(ctx, *source, *destination); err != nil {
		span.SetStatus(codes.Error, "database backup failed")
		logger.ErrorContext(ctx, "database backup failed")
		return errors.New("database backup failed; check source, new output path, and filesystem permissions")
	}
	logger.InfoContext(ctx, "database backup completed")
	_, err = fmt.Fprintln(output, "Backup verified. Copy it off the database host before upgrading.")
	return err
}
