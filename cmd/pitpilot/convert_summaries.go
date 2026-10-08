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
	"github.com/TheOutdoorProgrammer/pitpilot/internal/summarymetrics"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

func convertSummaries(args []string, output io.Writer) (runErr error) {
	f := flag.NewFlagSet("convert-summary-metrics", flag.ContinueOnError)
	f.SetOutput(output)
	database := f.String("database", "", "local PitPilot database for a rehearsal")
	server := f.String("server", "", "HTTPS PitPilot server origin")
	tokenFile := f.String("token-file", os.Getenv("PITPILOT_API_TOKEN_FILE"), "file containing API token")
	apply := f.String("apply", "", "apply using the exact preview token; omitted means preview only")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || (*database == "") == (*server == "") {
		return errors.New("provide exactly one of --database or --server; use --help")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	logger, shutdown, err := telemetry.StartTo(ctx, version, os.Stderr)
	if err != nil {
		return errors.New("conversion telemetry initialization failed")
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if shutdown(stop) != nil {
			runErr = errors.Join(runErr, errors.New("conversion telemetry shutdown failed"))
		}
	}()
	ctx, span := otel.Tracer("pitpilot/migration").Start(ctx, "migration.summary_metrics.cli")
	defer span.End()
	defer func() {
		if runErr != nil {
			span.SetStatus(codes.Error, "summary conversion failed")
			logger.ErrorContext(ctx, "summary conversion failed")
		}
	}()
	var report summarymetrics.Report
	if *database != "" {
		store, err := garage.Open(*database)
		if err != nil {
			return errors.New("cannot open rehearsal database")
		}
		defer store.Close()
		report, err = summarymetrics.Convert(ctx, store, *apply)
		if err != nil {
			return errors.New("summary conversion failed; source notes retained")
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
		body, _ := json.Marshal(map[string]string{"previewToken": *apply})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(*server, "/")+"/api/v1/migrations/summary-metrics/"+mode, bytes.NewReader(body))
		if err != nil {
			return errors.New("cannot create conversion request")
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+credential)
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
		client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err != nil {
			return errors.New("conversion request failed; check connectivity then run preview before retrying")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("server rejected conversion (HTTP %d); run preview again", response.StatusCode)
		}
		result, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil || json.Unmarshal(result, &report) != nil {
			return errors.New("invalid conversion response")
		}
	}
	if decoded, err := hex.DecodeString(report.PreviewToken); err != nil || len(decoded) != 32 || report.Converted < 0 || report.Skipped < 0 || report.Preserved < 0 || report.Applied != (*apply != "") {
		return errors.New("incomplete conversion report")
	}
	logger.InfoContext(ctx, "summary conversion completed", "applied", report.Applied, "converted", report.Converted, "preserved", report.Preserved)
	return json.NewEncoder(output).Encode(report)
}
