package main

import (
	"bytes"
	"context"
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
	"github.com/TheOutdoorProgrammer/pitpilot/internal/lubelogger"
	"go.opentelemetry.io/otel"
)

func migrate(args []string, output io.Writer) error {
	f := flag.NewFlagSet("migrate-lubelogger", flag.ContinueOnError)
	f.SetOutput(output)
	input := f.String("input", "", "private JSON export from tools/lubelogger-extract")
	source := f.String("source", "", "stable source instance name; reuse on every import")
	zone := f.String("timezone", "", "source server timezone, for example UTC")
	currency := f.String("currency", "", "verified source currency; currently USD only")
	distance := f.String("distance-unit", "", "verified source distance unit; currently mi only")
	fuel := f.String("fuel-unit", "", "verified source volume unit; currently us-gal only")
	database := f.String("database", "", "local PitPilot database, for an isolated rehearsal")
	server := f.String("server", "", "HTTPS PitPilot server URL")
	tokenFile := f.String("token-file", os.Getenv("PITPILOT_API_TOKEN_FILE"), "file containing API token; never pass token values as arguments")
	apply := f.String("apply", "", "apply using the exact token from a successful preview; omitted means preview only")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *input == "" || (*database == "") == (*server == "") {
		return errors.New("provide --input and exactly one of --database or --server; use --help")
	}
	file, err := os.Open(*input)
	if err != nil {
		return errors.New("cannot read source export")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return errors.New("source export exceeds 4 MiB limit or cannot be read")
	}
	var ex lubelogger.Export
	if err = json.Unmarshal(data, &ex); err != nil {
		return errors.New("invalid source export JSON")
	}
	ex, excluded := lubelogger.Sanitize(ex)
	request := lubelogger.Request{Options: lubelogger.Options{Source: *source, Timezone: *zone, Currency: *currency, DistanceUnit: *distance, FuelUnit: *fuel}, Export: ex, PreviewToken: *apply}
	batch, summary, err := lubelogger.Convert(request)
	if err != nil {
		return err
	}
	summary.ExcludedSecurityRecords = excluded
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ctx, span := otel.Tracer("pitpilot/migration").Start(ctx, "migration.lubelogger.cli")
	defer span.End()
	if *database != "" {
		store, err := garage.Open(*database)
		if err != nil {
			return errors.New("cannot open rehearsal database")
		}
		defer store.Close()
		report, err := store.Import(ctx, batch, *apply)
		if err != nil && !errors.Is(err, garage.ErrImportConflict) {
			return errors.New("migration transaction failed")
		}
		if e := json.NewEncoder(output).Encode(struct {
			garage.ImportReport
			Source lubelogger.Summary `json:"source"`
		}{report, summary}); e != nil {
			return e
		}
		return err
	}
	u, err := url.Parse(*server)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("server must be an HTTPS origin without credentials, query or path")
	}
	if *tokenFile == "" {
		return errors.New("--token-file or PITPILOT_API_TOKEN_FILE is required")
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
	body, _ := json.Marshal(request)
	if len(body) > 4<<20 {
		return errors.New("migration request exceeds 4 MiB limit")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(*server, "/")+"/api/v1/migrations/lubelogger/"+mode, bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot create migration request")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+credential)
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(httpRequest)
	if err != nil {
		return errors.New("migration request failed; check connectivity before retrying preview")
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return errors.New("cannot read migration response")
	}
	if response.StatusCode != 200 && response.StatusCode != 409 {
		return fmt.Errorf("server rejected migration (HTTP %d); no successful import was reported", response.StatusCode)
	}
	var report struct {
		garage.ImportReport
		Source lubelogger.Summary `json:"source"`
	}
	if json.Unmarshal(result, &report) != nil {
		return errors.New("invalid migration report")
	}
	report.Source.ExcludedSecurityRecords += excluded
	if err = json.NewEncoder(output).Encode(report); err != nil {
		return err
	}
	if response.StatusCode == 409 {
		return garage.ErrImportConflict
	}
	return nil
}
