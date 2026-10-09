package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

func discardLegacySignals(args []string, output io.Writer) (runErr error) {
	f := flag.NewFlagSet("discard-legacy-signals", flag.ContinueOnError)
	f.SetOutput(output)
	database := f.String("database", "", "existing PitPilot database; back up before applying")
	apply := f.String("apply", "", "apply the exact preview token; omitted means preview only")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *database == "" {
		return errors.New("provide --database; use --help")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	logger, shutdown, err := telemetry.StartTo(ctx, version, os.Stderr)
	if err != nil {
		return errors.New("discard telemetry initialization failed")
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if shutdown(stop) != nil {
			runErr = errors.Join(runErr, errors.New("discard telemetry shutdown failed"))
		}
	}()
	ctx, span := otel.Tracer("pitpilot/migration").Start(ctx, "migration.legacy_signals.discard")
	defer span.End()
	defer func() {
		if runErr != nil {
			span.SetStatus(codes.Error, "legacy signal discard failed")
			logger.ErrorContext(ctx, "legacy signal discard failed")
		}
	}()
	info, err := os.Stat(*database)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("database must be an existing regular file")
	}
	store, err := garage.Open(*database)
	if err != nil {
		return errors.New("cannot open database for legacy signal discard")
	}
	defer store.Close()
	report, err := store.DiscardLegacySignals(ctx, *apply)
	if err != nil {
		if errors.Is(err, garage.ErrLegacyDiscardConflict) {
			return garage.ErrLegacyDiscardConflict
		}
		return errors.New("legacy signal discard failed; preview again to confirm retained state")
	}
	logger.InfoContext(ctx, "legacy signal discard completed", "applied", report.Applied, "vehicles", report.VehiclesAffected, "policies_created", report.PoliciesCreated, "lubelogger_samples_deleted", report.LubeLoggerSamplesDeleted, "lubelogger_contexts_deleted", report.LubeLoggerContextsDeleted, "receiver_samples_deleted", report.ReceiverSamplesDeleted, "receiver_contexts_deleted", report.ReceiverContextsDeleted, "receiver_archives_deleted", report.ReceiverArchivesDeleted, "reused_samples_preserved", report.ReusedSamplesPreserved, "reused_contexts_preserved", report.ReusedContextsPreserved)
	return json.NewEncoder(output).Encode(report)
}
