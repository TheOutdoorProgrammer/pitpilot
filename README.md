# PitPilot

Vehicle maintenance and connected vehicle data, with a Go backend, native iOS app, and the picollector Raspberry Pi collector.

PitPilot brings maintenance records, driving history and vehicle measurements into one application, with a native garage and a persistent Go API. Built-in Smartcar and Raspberry Pi collection share the same measurement history. The full replacement scope is tracked in the [feature checklist](FEATURES.md).

The Raspberry Pi collector pairs to a vehicle from the app, queues observations offline, and installs signed automatic updates with health rollback. Initial Linux installation and OBD adapter setup require administrator access; the app handles pairing, status, revocation, and pausing updates. See [collector setup](docs/picollector.md) and [Smartcar configuration](docs/smartcar.md).

## Status

The native vehicle dashboard includes tappable chart previews, inline identifiers, private vehicle photos and plain-language measurement explanations. Settings and integrations live in its hamburger menu. Numeric trends, Off/On and code lanes, count bars and readable date axes retain source, units, quality and actual observation time. Date-only snapshots remain dates, and older readings stay visibly historical. See the [vehicle measurements guide](docs/vehicle-signals.md).

Opt-in USB GPS collection records actual fixes, queues them offline and derives previous trips with explicit recording gaps. The native map shows recorded routes and last-known location. OBD speed does not fabricate coordinates, and GPS-derived trip distance does not replace a measured odometer. Read the [GPS setup and privacy guide](docs/gps.md) before enabling recording; physical receiver and real-drive acceptance remain necessary for each installation.

[LubeLogger migration](docs/lubelogger-migration.md) previews changes and applies them atomically, with duplicate prevention and conflict reporting. Supported source data and import blockers are documented in the migration guide.

The iOS app stores its access token in Keychain and caches records, private photos and loaded measurement history for offline reading. It does not queue offline changes. Install [0.5.0, build 22 through Fledge](https://fledge.theoutdoorprogrammer.com/a/com.theoutdoorprogrammer.pitpilot/4fb42f27c459) on a device included in the provisioning profile. The [release history](https://github.com/TheOutdoorProgrammer/pitpilot/releases) and [feature checklist](FEATURES.md) track publication and verified behavior; apps older than 0.2.0 cannot read the imported record categories.

The release includes the cancellation fix from 0.1.1. New refreshes replace older work, while genuine transport failures report a bounded diagnostic category through the authenticated telemetry relay.

Receipt attachments, push notifications, route replay controls and CrewChief AI remain unfinished. Existing vehicle integrations should continue running until their collector handoff, vehicle compatibility and data-preservation checks pass. A legacy receiver may still be required for Home Assistant even after LubeLogger is retired.

## What we're building

- A garage with maintenance history, fuel and charging records, receipts, reminders, and ownership costs.
- A native iOS app with offline access, receipt capture, and trip maps.
- Built-in Smartcar support and a Raspberry Pi collector with durable offline storage.
- Trip recording and replay, followed by route comparisons and a map of roads traveled.
- Import from LubeLogger and exports that keep vehicle history portable.

Every reading should identify its source and when it was observed. Estimated mileage, inferred fuel-ups, and suggested maintenance must remain distinguishable from measured data and confirmed records.

The [vehicle measurements guide](docs/vehicle-signals.md) documents native signal ingestion, dashboard history, private OpenMetrics scraping and conversion of generated summaries. Pi and Smartcar adapters preserve actual measurement times, units and source identity. Missing readings remain unavailable, and estimated values remain labeled.

## CrewChief AI

CrewChief is an optional paid AI offering planned for PitPilot. Proposed capabilities include answering questions about vehicle history, extracting draft records from receipts, explaining diagnostic observations, and suggesting maintenance to review.

Answers should cite the records or documentation they use and acknowledge missing evidence. Changes to maintenance history require confirmation. Location history is shared with an AI provider only when the user explicitly chooses to include it.

The proposed product split keeps ordinary recordkeeping, device ingestion, maps, and data export usable without an AI subscription. Pricing, usage limits, model providers, and distribution terms remain undecided. Third-party vehicle connectivity may have separate costs and requirements.

## Technical direction

The backend uses Go and SQLite. Deploy one replica with a persistent volume; the API accepts a high-entropy household token. The native app uses SwiftUI and MapKit. See the [API contract](docs/api.md) and [architecture decision](adr/0001-start-with-a-single-household-go-service-and-native-ios-clie.md).

The household authentication model grants access to the whole garage. Per-person accounts and vehicle permissions remain planned. Collectors use separate, revocable credentials that can only upload to their paired vehicle. Smartcar application credentials stay on the server; the native app uses its authorization session and explicit vehicle selection.

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

iOS CI and release jobs use a repository-scoped, self-hosted macOS ARM64 runner with Xcode, an installed iPhone 17 simulator runtime, XcodeGen, jq, and Ruby with Minitest. The test helper isolates build files and results per run, creates and removes its own simulator, and forwards cancellation to its child processes. Fork pull requests run backend checks; native CI runs after changes reach a trusted repository branch. Signing uses a temporary keychain and restores the runner's prior keychain state.

Self-hosters can build locally with `goreleaser build --snapshot --clean`, then `docker build --build-arg TARGETARCH=amd64 -t pitpilot:local .`. Mount the database directory, supply a token secret, and run only one server against a database. Container publishing and deployment promotion are separate operations; use the published digest in GitOps.

## Location and vehicle support

Route history requires timestamped location observations. OBD speed alone cannot establish a route. GPS hardware or phone-based recording must be validated for the Pi workflow.

Smartcar signal availability and update frequency vary by vehicle and manufacturer. Compatibility and recording gaps must be visible in the app; detailed route coverage is not yet verified.

## Development and licensing

Work is tracked in [FEATURES.md](FEATURES.md). An item is complete when its acceptance checks pass, including relevant failure cases and verification in the environment where it runs.

Licensing and contribution terms will be selected before accepting code contributions. A public repository alone does not grant an open-source license. Initial documentation was drafted with AI assistance.
