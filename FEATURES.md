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
- [x] Evaluate existing Go collectors and integrations for reuse, including licensing and separation from deployment-specific configuration. See [collector notices](deploy/picollector/THIRD-PARTY-NOTICES.txt) and [integration architecture](adr/0004-use-scoped-collectors-and-application-owned-smartcar-connect.md).

## Replacement baseline

### Vehicle dashboard and GPS release

- [x] Move settings, vehicle editing, Smartcar and Pi management into a native hamburger menu.
- [x] Edit VIN and license plate, and upload or remove a private vehicle photo with embedded metadata removed.
- [x] Show vehicle identifiers directly in the vehicle card.
- [x] Show tappable chart previews on the vehicle dashboard, retaining measurement type, source, quality and time precision.
- [x] Explain every catalog measurement and its state labels in plain language, including pressure units and reference points.
- [x] Derive current mileage from actual readings and subsequent immutable Pi distance increments; handle retries, corrections and reordered uploads.
- [x] Capture validated USB GPS fixes independently of OBD polling and queue them durably for offline upload.
- [x] Derive automatic trips with explicit recording gaps and show previous routes and actual last-known vehicle location.
- [x] Offer Pi recording controls, trip deletion and location-history deletion that delayed uploads cannot undo.
- [x] Refresh changed generated LubeLogger summaries explicitly, preserving previous source documents and measurements in versioned archives.
- [ ] Verify these new native journeys on the self-hosted runner and publish the signed release.
- [ ] Reconcile the final household LubeLogger snapshot and retire the old deployment after verified recovery backups.
- [ ] Validate the purchased USB receiver on the physical Pi and complete a real drive through delayed upload and native map display.

The [GPS guide](docs/gps.md), [API contract](docs/api.md), [measurement guide](docs/vehicle-signals.md) and [migration workflow](docs/lubelogger-migration.md) describe the implemented contracts. Physical receiver reception, drive acceptance and final publication remain separate checks. The broader unchecked items below include functionality beyond this release.

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

### Native vehicle measurements

- [x] Store canonical readings from Pi and Smartcar sources with idempotent ingestion, source time, units and quality.
- [x] Preserve date-only snapshots and daily aggregates separately from timestamped samples.
- [x] Show available values on the native vehicle dashboard with provenance and stale-state labels.
- [x] Open a historical chart from each metric, retaining ranges and gaps when history is downsampled.
- [x] Choose charts by measurement type: filled numeric trends, boolean and code lanes, and count or total bars with readable date axes.
- [x] Provide an opt-in OpenMetrics endpoint with a separate read-only credential.
- [x] Convert complete generated driving and status notes transactionally, preserving structured diagnostics and original evidence before removing visible notes.

Native measurements were introduced in [v0.3.0](https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.3.0). Chart improvements shipped in [v0.3.1](https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.3.1). Its [release workflow](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37849807248) passed backend checks, 29 native unit tests, seven UI journeys, and 16 runner cleanup fixtures. Native builds and signing ran on the self-hosted runner.

Fresh screenshots verified dense fuel trends, visible count bars, boolean and code lanes, readable date labels, range details, offline history, and landscape capture. Physical-device validation of the updated charts remains pending. A single-day aggregate can repeat its date across several ticks; its full period times remain available in the details.

Deployed acceptance verified ingestion, retry and conflict behavior, preserved history ranges, valid zero readings, independent scraper authorization, and correlated operational telemetry. Generated-note conversion passed source reconciliation and preservation checks; repeat conversion had no work, and a restored backup skipped unchanged source imports without recreating notes. Built-in adapters and their separate live acceptance requirements are tracked below.

### Raspberry Pi integration

- [ ] Install and enroll a collector through a documented, repeatable process; validate app-guided provisioning.
- [x] Pair a device with the household and vehicle using single-use enrollment and revocable, vehicle-scoped credentials.
- [ ] Discover and pair supported OBD adapters, showing which readings the vehicle supports.
- [x] Collect vehicle observations with timestamps, units, source identity, and explicit clock-related sampling gaps.
- [ ] Configure supported connectivity options through guided setup and test reconnect behavior.
- [x] Persist observations offline and upload them with retry, acknowledgement, and duplicate prevention, including independent delivery to an existing receiver.
- [ ] Recover from power loss, intermittent adapters, incorrect clocks, and exhausted local storage without silently losing acknowledged data.
- [x] Show connection state, queued data, last observation, last upload, and actionable recovery guidance in the app.
- [x] Let an enrolled Pi check for, download, verify, and install signed collector releases, with compatibility and health checks before acceptance.
- [x] Preserve queued observations and configuration during automatic updates; recover from interrupted activation through a separate trusted updater and roll back failed health checks.
- [ ] Verify native version, update status, failures, and pause controls on a physical collector; add an explicit retry control for failed updates.
- [ ] Provide useful diagnostics without exposing credentials, location history, or personal records in operational logs.

### Smartcar integration

- [ ] Connect, assign, reconnect, and disconnect a vehicle through Smartcar's supported authorization flow.
- [ ] Discover supported signals and show unavailable, stale, or delayed data clearly.
- [x] Implement application-owned Connect sessions, explicit vehicle binding, reconnect, local detach, and encrypted private integration storage, with provider fixtures and API regression coverage.
- [x] Reconcile current signals with durable polling leases, idempotent observations, throttling, and retry deadlines.
- [ ] Authenticate incoming updates, handle retries and duplicates, and recover from missed deliveries.
- [x] Normalize supported vehicle signals while preserving source units and actual OEM observation timestamps; reject unavailable or untimed readings.
- [ ] Turn supported observations into mileage updates and reviewable suggestions; inferred events must not silently become confirmed purchases or completed work.
- [ ] Validate actual signal availability, location cadence, connectivity requirements, and operating costs on supported vehicles.

The [collector tests](internal/picollector) cover durable dual delivery, rejected empty observations, clock gating, revocation, signed artifact validation, interrupted activation, and rollback after multiple upgrades. The initial profile is read-only J1850 VPW over an already configured serial/RFCOMM adapter. The first physical installation and exclusive handoff from the old collector have passed. Physical power-loss recovery and guided Bluetooth/network setup remain acceptance work. See [collector setup](docs/picollector.md).

[Smartcar tests](internal/smartcar) cover authorization ownership, reconnect preservation, missing timestamps, unsupported signals, provider failures, and bounded reconciliation. Live connection requires the operator's application UUID, mounted credentials, and valid OEM consent. Polling does not implement webhooks or reconstruct driven routes. See [Smartcar setup](docs/smartcar.md).

[v0.4.0](https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.4.0) ships these integration foundations and [signed iOS build 13](https://fledge.theoutdoorprogrammer.com/a/com.theoutdoorprogrammer.pitpilot/9b1d5bf4c5c0). The self-hosted runner passed 39 unit tests and 10 UI journeys. Go race tests, vet and vulnerability checks passed before Quill built the GoReleaser artifacts and container. Both collector manifests and container platforms were verified. Deployed acceptance passed 27 integration checks, 18 measurement/authentication checks and 127 existing-data comparisons. All eight existing data tables were preserved, and Grafana verified correlated backend and synthetic native telemetry.

Physical collector installation and live Smartcar authorization have been exercised. The old collector is disabled, while the existing receiver remains necessary for Home Assistant delivery. The old Kubernetes Smartcar sync was retired after its credentials were reused and its vehicle mapping and state were backed up. Real power-loss acceptance and successful fresh OEM signal retrieval remain open; authorization alone does not prove provider recovery.

[v0.4.2](https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.4.2) adds backend odometer calibration and hourly Smartcar reconciliation. Its [release](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37877157972) passed 41 native unit tests and 10 UI journeys on the self-hosted runner. Live checks verified corrected readings, distance accumulation, duplicate protection, reordered uploads, edits and deletion. All original database rows survived the schema upgrade. Manual Smartcar sync preserves the hourly deadline; the current provider connection still requires recovery, which remains visible in the app.

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
- [x] Make imports repeatable without duplicates and report unsupported data before cutover.
- [ ] Export vehicle records, attachments, and recorded tracks in documented formats.
- [ ] Preserve existing Home Assistant integration through a supported interface and validate MQTT discovery where used.
- [ ] Provide a documented authenticated API and integration events or webhooks.
- [ ] Back up and restore records, attachments, device identities, and integration state; verify restoration on a clean installation.

Migration validation: [CI](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37828421701) passes backend race tests, static checks, vulnerability scanning, GoReleaser builds, the runtime image build and synthetic LiteDB extraction checks. All 18 native unit tests and four UI journeys pass on the self-hosted runner, including imported note editing, metadata preservation, estimated readings, planned work and recurring reminder completion. Reviewed simulator screenshots confirm the imported values and source details are visible. Storage tests cover atomic conflict rejection, repeat imports, pinned interpretation settings and online backup restoration. This does not complete the broader attachments, units and integration requirements above.

[Version 0.2.0](https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.2.0) passed the same native suite during [release](https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37829804233). The signed app is 0.2.0, build 3; the Fledge download matches the verified staged IPA. Quill built Linux and macOS binaries with GoReleaser before Docker copied and checked the Linux binaries. The downloaded macOS CLI passed checksum and runtime-version checks.

Deployed acceptance exercised preview, application, repeat application, sparse edits, metadata preservation, and rejection of an entire mixed batch containing a conflict. The synthetic vehicle was removed afterward. A new online backup was copied off the database host, checked for integrity, restored separately, and opened successfully by the released CLI. Grafana received correlated migration and backup spans and logs with version 0.2.0; inspected telemetry contained route templates and counts without record contents or identifiers. Install the new native app before importing an existing garage.

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
