# Feature checklist

Unchecked items are planned. This checklist separates the replacement baseline from later enhancements and the optional paid AI offering. Items can be split into issues as implementation begins; completed items should link to their verification evidence.

## First usable release

These smaller milestones track the first deployment without claiming full completion of the replacement baseline below.

- [x] Implement persistent vehicle CRUD with authenticated household access and regression coverage for concurrent edits.
- [x] Record service, repairs, upgrades, fuel, expenses, and notes with validated dates, mileage, and integer costs.
- [x] Create date or mileage reminders and mark them complete.
- [x] Store recorded trips with validated GPS coordinates and provide a consistent household JSON export.
- [x] Verify secure native connection, vehicle creation, service entry, history, and the trips empty state on standard and compact iPhone simulators.
- [x] Verify populated native trip maps, reminder completion, and cached reading during a server outage.
- [x] Publish signed iOS builds through Quill and Fledge.
- [x] Build backend binaries with GoReleaser inside Quill and copy them into the runtime container.
- [x] Deploy the image through Flux and verify authenticated behavior and persistence after restart.
- [x] Verify backend and relayed native request observations in Grafana without personal data in telemetry.

Backend validation: `go test -race ./...`, `go vet ./...`, and `govulncheck`; the end-to-end API tests cover record lifecycle, export, authentication, validation, persistence, concurrent edits, and the client telemetry allowlist. These checks gate publication in the [Release workflow](.github/workflows/release.yml).

Native validation: six [unit tests](ios/PitPilotTests/PitPilotTests.swift) and the basic [garage UI journey](ios/PitPilotUITests/PitPilotUITests.swift) pass on iPhone 17 and iPhone SE simulators. The expanded iPhone 17 suite also verifies reminder completion, a synthetic recorded route rendered in MapKit, and cached vehicle, service, and reminder reads after offline relaunch with writes disabled. The full suite passed on the repository's self-hosted macOS runner in both [CI](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37798195739) and the [first release](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37815555940).

Release validation: [v0.1.0](https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.1.0) includes Linux AMD64 and ARM64 binaries built by GoReleaser through Quill. The runtime image copies those artifacts and checks their version. The signed iOS app is version 0.1.0, build 1; strict signature verification passed, and the IPA downloaded from Fledge matches the staged artifact's SHA-256. Ad Hoc installation requires a device included in the provisioning profile.

Deployment validation: the immutable release passed HTTPS and authentication checks after Flux reconciliation. A synthetic vehicle and service record survived replacement of the running pod, then were removed. Grafana received correlated request, datastore, and native-relay observations with version 0.1.0 and route templates. Inspected trace attributes and log fields contained no record identifiers, coordinates, bodies, credentials, or query strings. The native relay was exercised with a synthetic client event; a physical phone session has not been verified. Retained local storage is not an off-node backup.

Connection recovery: [v0.1.1](https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.1.1) preserves cached data and connection state when a refresh is cancelled, and prevents stale refreshes from overwriting newer results. Eight new regression tests cover cancellation, replacement refresh ownership, real offline failures, and recovery. All 14 unit tests and both UI journeys passed on the self-hosted runner in [CI](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37819513238) and [release](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37820884786). The signed Fledge build is version 0.1.1, build 2. A device update is required to receive this fix.

## Product decisions

- [x] Confirm PitPilot, paid CrewChief AI, and repository ownership under TheOutdoorProgrammer.
- [ ] Select the core license, contribution terms, and CrewChief distribution model.
- [ ] Confirm which capabilities are included without an AI subscription, including self-hosting and native app distribution.
- [ ] Identify supported vehicle, OBD adapter, Raspberry Pi, GPS, and iOS combinations through testing.
- [ ] Audit current LubeLogger features and export formats; map every feature to a checklist item or an explicitly agreed exclusion.
- [ ] Evaluate existing Go collectors and integrations for reuse, including licensing and separation from deployment-specific configuration.

## Replacement baseline

### Garage and records

- [ ] Manage multiple vehicles, identifiers, photos, preferred units, and odometer baselines or corrections.
- [ ] Record service, repairs, upgrades, dates, mileage, costs, notes, tags, and attachments.
- [ ] Track fuel purchases and EV charging, including partial fills, consumption, unit conversions, and corrections.
- [ ] Track taxes, registration, insurance, and other ownership expenses.
- [ ] Schedule recurring maintenance by date, mileage, or whichever comes first; retain completion history.
- [ ] Plan work with priorities, status, costs, and links to resulting service records.
- [ ] Track supplies, parts, equipment, installation or removal, and usage against work performed.
- [ ] Record inspections with criteria and results.
- [ ] Support searchable notes, custom fields, record filters, and useful bulk operations.
- [ ] Show costs, fuel economy, mileage trends, upcoming maintenance, and exportable vehicle reports.
- [ ] Support household collaboration with vehicle-level permissions and a history of record changes.

### Native iOS app

- [ ] Provide garage, maintenance, integration status, and trip views in a native app.
- [ ] Read cached records and queue new records or attachments offline; handle sync conflicts without silent data loss.
- [ ] Capture receipts and documents with the camera or file picker and attach them to records.
- [ ] Deliver configurable maintenance and integration notifications without repeated alert noise.
- [ ] Support accessibility, dynamic text, preferred units, local dates, and time zones.

### Raspberry Pi integration

- [ ] Install and enroll a collector through a documented, repeatable process; validate app-guided provisioning.
- [ ] Pair a device with an account and vehicle using revocable credentials.
- [ ] Discover and pair supported OBD adapters, showing which readings the vehicle supports.
- [ ] Collect vehicle observations with timestamps, units, source identity, and recording coverage.
- [ ] Configure supported connectivity options through guided setup and test reconnect behavior.
- [ ] Persist observations offline and upload them with retry, acknowledgement, and duplicate prevention.
- [ ] Recover from power loss, intermittent adapters, incorrect clocks, and exhausted local storage without silently losing acknowledged data.
- [ ] Show connection state, queued data, last observation, last upload, and actionable recovery guidance in the app.
- [ ] Let the Pi automatically check for, download, verify, and install signed collector releases without SSH or manual installation, with compatibility checks before activation.
- [ ] Preserve queued observations and configuration during automatic updates; recover from interrupted installation and automatically roll back if the updated collector fails its health checks.
- [ ] Show the installed version, update status, and failures in the app, with controls to pause automatic updates or retry a failed update.
- [ ] Provide useful diagnostics without exposing credentials, location history, or personal records in operational logs.

### Smartcar integration

- [ ] Connect, assign, reconnect, and disconnect a vehicle through Smartcar's supported authorization flow.
- [ ] Discover supported signals and show unavailable, stale, or delayed data clearly.
- [ ] Authenticate incoming updates, handle retries and duplicates, and recover from missed deliveries.
- [ ] Normalize vehicle data while preserving source values, units, and observation timestamps.
- [ ] Turn supported observations into mileage updates and reviewable suggestions; inferred events must not silently become confirmed purchases or completed work.
- [ ] Validate actual signal availability, location cadence, connectivity requirements, and operating costs on supported vehicles.

### Trip history and maps

- [ ] Validate a GPS source for the Pi workflow; evaluate phone recording separately, including background behavior and battery use.
- [ ] Detect trips and stops from available evidence, tolerating delayed uploads, missing observations, and clock changes.
- [ ] Show trip distance, duration, stops, source, and recording coverage; allow correction, splitting, merging, and deletion.
- [ ] Display recorded routes and replay a drive on the native map, with explicit gaps and accuracy information.
- [ ] Associate vehicle observations with a trip without confusing estimated distance with a measured odometer.
- [ ] Filter history by vehicle and date; annotate trips and export recorded tracks.
- [ ] Provide explicit recording controls, location permissions, retention settings, and deletion of derived location data.

### Migration and interoperability

- [x] Implement typed LiteDB extraction, protected source preservation, preview/apply reconciliation and conflict detection. See [the migration workflow](docs/lubelogger-migration.md) for supported data and explicit blockers.
- [x] Support undated notes, typed custom fields, estimated odometer readings and planned work in the migration projection and native app.
- [ ] Import LubeLogger vehicles, record types, attachments, custom fields, units, and relationships with a preview and reconciliation report.
- [ ] Make imports repeatable without duplicates and report unsupported data before cutover.
- [ ] Export vehicle records, attachments, and recorded tracks in documented formats.
- [ ] Preserve existing Home Assistant integration through a supported interface and validate MQTT discovery where used.
- [ ] Provide a documented authenticated API and integration events or webhooks.
- [ ] Back up and restore records, attachments, device identities, and integration state; verify restoration on a clean installation.

### Operation and replacement acceptance

- [ ] Document installation, upgrades, database migrations, recovery, and supported deployment configurations.
- [ ] Authenticate users and devices, enforce vehicle permissions, protect credentials, and test revocation.
- [ ] Instrument requests, jobs, sync, storage, and external calls with structured logs and correlated traces; verify telemetry in a deployed environment.
- [ ] Keep coordinates, vehicle identifiers, receipts, message content, and credentials out of operational telemetry.
- [ ] Run meaningful automated checks for financial totals, units, reminders, ingestion, permissions, migration, and offline recovery.
- [ ] Verify a complete real drive from collection through delayed upload to trip display and a linked maintenance or receipt record.
- [ ] Reconcile imported records and attachments, verify both integration types, and exercise backup recovery before replacing LubeLogger.

## Later enhancements

- [ ] Group repeated drives into recognizable routes while preserving distinct route variants.
- [ ] Compare routes by duration, distance, stops, and fuel or energy use where measurements support it.
- [ ] Show roads traveled and visit frequency over a chosen period.
- [ ] Compare vehicle health observations before and after maintenance.
- [ ] Add widgets and shortcuts for common actions after the core app workflow is proven.

## CrewChief AI: optional paid offering

- [ ] Answer questions about a user's vehicles using authorized records, with links to supporting evidence.
- [ ] Extract proposed fuel, expense, or service records from receipts and invoices, requiring review before saving.
- [ ] Explain diagnostic observations and suggest next checks using applicable vehicle context and cited reference material.
- [ ] Suggest maintenance from history, usage, and documented schedules, distinguishing recommendations from confirmed work.
- [ ] Summarize changes and unusual readings while accounting for missing coverage and measurement quality.
- [ ] Require confirmation for record changes; treat instructions embedded in receipts, documents, and external data as untrusted input.
- [ ] Make AI use explicit, disclose provider data handling, minimize shared data, and require separate consent to include location history.
- [ ] Define subscription entitlements, usage limits, visible costs, billing, cancellation, and behavior during provider outages.
- [ ] Keep records and exports accessible after cancellation; test the core application with AI disabled.
- [ ] Evaluate answers against known records and diagnostic examples, including unsupported conclusions and missing evidence, before paid release.

## Reference material

- [LubeLogger documentation](https://docs.lubelogger.com/) for the parity audit.
- [Smartcar vehicle signals](https://smartcar.com/product/signals) for signal compatibility.
- [Smartcar API v3 overview](https://smartcar.com/blog/introducing-smartcar-api-v3) for integration behavior and manufacturer-dependent update frequency.
