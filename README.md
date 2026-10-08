# PitPilot

Vehicle maintenance, trip history, and connected vehicle data, with a Go backend and a native iOS app.

PitPilot brings maintenance records and driving history into one application. The first release provides a native garage and a persistent Go API. Built-in Smartcar and Raspberry Pi collection are planned next; the full replacement scope is tracked in the [feature checklist](FEATURES.md).

The planned Raspberry Pi integration includes app-guided setup, offline collection, and automatic signed updates with rollback. Routine setup and troubleshooting should not require SSH.

## Status

Early development. The current implementation supports vehicles, service and fuel records, reminders, recorded-trip display, and JSON export. The iOS app stores its access token in Keychain and caches records for offline reading. It does not queue offline changes.

Automatic vehicle ingestion, receipt attachments, recurring reminders, push notifications, LubeLogger migration, and CrewChief AI remain unfinished. Existing vehicle integrations should continue running until migration and replacement acceptance checks pass.

## What we're building

- A garage with maintenance history, fuel and charging records, receipts, reminders, and ownership costs.
- A native iOS app with offline access, receipt capture, and trip maps.
- Built-in Smartcar support and a Raspberry Pi collector with durable offline storage.
- Trip recording and replay, followed by route comparisons and a map of roads traveled.
- Import from LubeLogger and exports that keep vehicle history portable.

Every reading should identify its source and when it was observed. Estimated mileage, inferred fuel-ups, and suggested maintenance must remain distinguishable from measured data and confirmed records.

## CrewChief AI

CrewChief is an optional paid AI offering planned for PitPilot. Proposed capabilities include answering questions about vehicle history, extracting draft records from receipts, explaining diagnostic observations, and suggesting maintenance to review.

Answers should cite the records or documentation they use and acknowledge missing evidence. Changes to maintenance history require confirmation. Location history is shared with an AI provider only when the user explicitly chooses to include it.

The proposed product split keeps ordinary recordkeeping, device ingestion, maps, and data export usable without an AI subscription. Pricing, usage limits, model providers, and distribution terms remain undecided. Third-party vehicle connectivity may have separate costs and requirements.

## Technical direction

The backend uses Go and SQLite. Deploy one replica with a persistent volume; the API accepts a high-entropy household token. The native app uses SwiftUI and MapKit. See the [API contract](docs/api.md) and [architecture decision](adr/0001-start-with-a-single-household-go-service-and-native-ios-clie.md).

The initial authentication model grants access to the whole household. Per-person accounts and vehicle permissions remain planned. A future Raspberry Pi collector will upload authenticated observations; existing collection code will be evaluated for reuse before that implementation begins.

## Run the backend

Install the Go version in `go.mod`. Create a token outside the checkout and start the service:

```sh
umask 077
mkdir -p "$HOME/.config/pitpilot"
openssl rand -hex 32 > "$HOME/.config/pitpilot/api-token"
export PITPILOT_API_TOKEN_FILE="$HOME/.config/pitpilot/api-token"
export PITPILOT_DB="$HOME/.local/share/pitpilot/pitpilot.db"
go run ./cmd/pitpilot
```

The service listens on port 8080. Put it behind HTTPS before connecting the native app. Enter the server URL and token in the app; never compile an access token into it. Rotating the token requires restarting the backend and reconnecting clients.

`PITPILOT_ADDR` changes the listen address. `PITPILOT_API_TOKEN` is also supported for deployments that inject secrets as environment variables. Set the standard `OTEL_EXPORTER_OTLP_ENDPOINT` and authentication headers in deployment secrets to export logs and traces. The app relays only bounded request observations through the authenticated API; it carries no general telemetry credentials.

The database directory contains personal records and location data. [Back it up and verify restoration](docs/api.md#export-and-backup) before upgrades. JSON export is available through the API; importing those exports is not implemented yet.

## Build and test

```sh
go test -race ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
brew install xcodegen jq
xcodegen generate --spec ios/project.yml
bash scripts/test-ios.sh
```

The Release workflow signs and stages the iOS app, then uses [Quill](https://github.com/TheOutdoorProgrammer/quill) to publish it through [Fledge](https://github.com/TheOutdoorProgrammer/fledge), build Go artifacts with GoReleaser, and publish the container. The Dockerfile copies the prebuilt Go artifacts; it never compiles Go. Release credentials and deployment endpoints are injected through repository secrets.

iOS CI and release jobs use a repository-scoped, self-hosted macOS ARM64 runner with Xcode, an installed iPhone 17 simulator runtime, XcodeGen, and jq. The test helper creates and removes its own simulator. Fork pull requests run backend checks; native CI runs after changes reach a trusted repository branch. Signing uses a temporary keychain and restores the runner's prior keychain state.

Self-hosters can build locally with `goreleaser build --snapshot --clean`, then `docker build --build-arg TARGETARCH=amd64 -t pitpilot:local .`. Mount the database directory, supply a token secret, and run only one server against a database. Container publishing and deployment promotion are separate operations; use the published digest in GitOps.

## Location and vehicle support

Route history requires timestamped location observations. OBD speed alone cannot establish a route. GPS hardware or phone-based recording must be validated for the Pi workflow.

Smartcar signal availability and update frequency vary by vehicle and manufacturer. Compatibility and recording gaps must be visible in the app; detailed route coverage is not yet verified.

## Development and licensing

Work is tracked in [FEATURES.md](FEATURES.md). An item is complete when its acceptance checks pass, including relevant failure cases and verification in the environment where it runs.

Licensing and contribution terms will be selected before accepting code contributions. A public repository alone does not grant an open-source license. Initial documentation was drafted with AI assistance.
