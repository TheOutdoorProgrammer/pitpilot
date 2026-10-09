package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

func recoverReceiver(args []string, output io.Writer) (runErr error) {
	f := flag.NewFlagSet("recover-receiver", flag.ContinueOnError)
	f.SetOutput(output)
	input := f.String("input", "", "closed legacy receiver bbolt backup")
	vehicle := f.String("vehicle", "", "existing PitPilot vehicle ID")
	database := f.String("database", "", "local PitPilot database for a rehearsal")
	server := f.String("server", "", "HTTPS PitPilot server origin")
	tokenFile := f.String("token-file", os.Getenv("PITPILOT_API_TOKEN_FILE"), "file containing API token")
	apply := f.String("apply", "", "apply the exact preview token; omitted means preview only")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *input == "" || *vehicle == "" || (*database == "") == (*server == "") {
		return errors.New("provide --input, --vehicle and exactly one of --database or --server; use --help")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	logger, shutdown, err := telemetry.StartTo(ctx, version, os.Stderr)
	if err != nil {
		return errors.New("recovery telemetry initialization failed")
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if shutdown(stop) != nil {
			runErr = errors.Join(runErr, errors.New("recovery telemetry shutdown failed"))
		}
	}()
	ctx, span := otel.Tracer("pitpilot/migration").Start(ctx, "migration.receiver_recovery.cli")
	defer span.End()
	defer func() {
		if runErr != nil {
			span.SetStatus(codes.Error, "receiver recovery failed")
			logger.ErrorContext(ctx, "receiver recovery failed")
		}
	}()
	_, readSpan := otel.Tracer("pitpilot/migration").Start(ctx, "receiver_backup.read")
	snapshot, err := receiverhistory.ReadClosed(*input)
	if err != nil {
		readSpan.SetStatus(codes.Error, "invalid receiver backup")
	}
	readSpan.End()
	if err != nil {
		return err
	}
	request := garage.ReceiverRecoveryRequest{VehicleID: *vehicle, Snapshot: snapshot, PreviewToken: *apply}
	var report garage.ReceiverRecoveryReport
	if *database != "" {
		sourceInfo, sourceErr := os.Stat(*input)
		targetInfo, targetErr := os.Stat(*database)
		if sourceErr != nil || (targetErr == nil && os.SameFile(sourceInfo, targetInfo)) {
			return errors.New("rehearsal database must differ from receiver backup")
		}
		store, err := garage.Open(*database)
		if err != nil {
			return errors.New("cannot open rehearsal database")
		}
		defer store.Close()
		report, err = store.RecoverReceiver(ctx, request)
		if err != nil {
			return errors.New("receiver recovery failed; run preview again to confirm retained state")
		}
	} else {
		u, err := url.Parse(*server)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("server must be an HTTPS origin without credentials, query or path")
		}
		token, err := os.ReadFile(*tokenFile)
		if err != nil {
			return errors.New("cannot read API token file")
		}
		credential := strings.TrimSpace(string(token))
		if len(credential) < 32 || strings.ContainsAny(credential, "\r\n") {
			return errors.New("invalid API token file")
		}
		mode := "preview"
		if *apply != "" {
			mode = "apply"
		}
		body, err := json.Marshal(request)
		if err != nil || len(body) > receiverhistory.MaxRequestBytes {
			return receiverhistory.ErrInvalid
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(*server, "/")+"/api/v1/migrations/receiver/"+mode, bytes.NewReader(body))
		if err != nil {
			return errors.New("cannot create recovery request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
		client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		externalCtx, externalSpan := otel.Tracer("pitpilot/migration").Start(ctx, "receiver_recovery.request")
		req = req.WithContext(externalCtx)
		otel.GetTextMapPropagator().Inject(externalCtx, propagation.HeaderCarrier(req.Header))
		response, err := client.Do(req)
		defer externalSpan.End()
		if err != nil {
			externalSpan.SetStatus(codes.Error, "receiver recovery request failed")
			return errors.New("recovery request failed; check connectivity and preview again before retrying")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			externalSpan.SetStatus(codes.Error, "receiver recovery rejected")
			return fmt.Errorf("server rejected recovery (HTTP %d); run preview again", response.StatusCode)
		}
		result, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil || len(result) > 1<<20 || json.Unmarshal(result, &report) != nil {
			return errors.New("invalid recovery response")
		}
	}
	decoded, err := hex.DecodeString(report.PreviewToken)
	if err != nil || len(decoded) != 32 || report.Events < 1 || report.Archived < 0 || report.Skipped < 0 || report.Events != report.Archived+report.Skipped || report.Applied != (*apply != "") {
		return errors.New("incomplete recovery report")
	}
	logger.InfoContext(ctx, "receiver recovery completed", "applied", report.Applied, "events", report.Events, "archived", report.Archived, "samples_created", report.SamplesCreated, "samples_reused", report.SamplesReused, "unknown_clock", report.UnknownClock)
	return json.NewEncoder(output).Encode(report)
}
