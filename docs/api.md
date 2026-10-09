# API v1

All `/api/v1` requests require `Authorization: Bearer <household-token>`. Use HTTPS. Keep the token in the iOS Keychain or an integration's secret store. Tokens are not user accounts: a token grants access to the entire household. Rotate the deployment secret and restart the server to revoke it.

Requests with bodies use `Content-Type: application/json`, except vehicle photo uploads described below. Responses are JSON, except photo downloads and successful resource deletes (`204`). Create returns `201` with the saved resource. Errors use `{"error":"message"}` and an appropriate HTTP status. Unknown fields, multiple JSON documents, invalid units, negative costs, and JSON bodies above 4 MiB are rejected. Client-supplied IDs on creation are replaced by server-generated IDs.

| Method | Path | Result |
| --- | --- | --- |
| GET, POST | `/api/v1/vehicles` | List or create vehicles |
| GET, PATCH, DELETE | `/api/v1/vehicles/{id}` | Read, edit, or delete a vehicle and its records |
| GET, PUT, DELETE | `/api/v1/vehicles/{id}/photo` | Download, replace, or remove the private vehicle photo |
| GET, POST | `/api/v1/vehicles/{id}/records` | List or create records |
| PATCH, DELETE | `/api/v1/records/{id}` | Edit or delete a record |
| GET, POST | `/api/v1/vehicles/{id}/reminders` | List or create reminders |
| PATCH, DELETE | `/api/v1/reminders/{id}` | Edit, complete or delete a reminder |
| GET, POST | `/api/v1/vehicles/{id}/trips` | List or create recorded trips |
| DELETE | `/api/v1/trips/{id}` | Delete a trip and its recorded points |
| GET | `/api/v1/vehicles/{id}/location` | Read the latest actual Pi GPS or Smartcar location, or `{"location":null}` |
| DELETE | `/api/v1/vehicles/{id}/location-history` | Clear native GPS fixes and automatic trips with replay protection |
| GET | `/api/v1/export` | Download a consistent JSON snapshot of household records |
| POST | `/api/v1/client-events` | Relay a bounded native request observation to server telemetry |
| POST | `/api/v1/migrations/lubelogger/preview` | Validate a typed export and preview reconciliation |
| POST | `/api/v1/migrations/lubelogger/apply` | Atomically apply the exact reviewed preview |

List responses are arrays. Missing resources return `404`; an existing resource with no children returns `[]`. Trip lists default to 50 entries, accept `limit` up to 100, and paginate with `before` (the last trip's RFC3339 `startedAt`) and `beforeId` (its opaque ID). Trips order by start time descending, then identity descending. Other resource lists retain their existing full-history behavior. Ordinary create requests have no idempotency key; clients must not automatically retry them after an ambiguous network failure.

## Resource fields

Vehicle photos use authenticated raw `image/jpeg` PUT requests, limited to 2 MiB and 1600 pixels per side. Invalid images return `422`; oversized requests return `413`. The server decodes and re-encodes every image, removing embedded EXIF location metadata and trailing content. PUT and DELETE return the updated vehicle (`200`); DELETE is idempotent for an existing vehicle. The server-owned `photoRevision` content hash is present only when a photo exists. GET returns JPEG bytes with an ETag and `Cache-Control: no-store`; a missing photo returns `404`. These endpoints use the household token, never a public asset URL or collector token. SQLite backups and the JSON export's `vehiclePhotos` collection include photo bytes. JSON encodes those bytes as base64. Deleting the vehicle also deletes its photo.

All IDs are opaque strings. Mileage is explicitly in miles, liquid volume in US gallons, and costs are integer cents. Currency conversion and multiple currencies are not implemented. Calendar dates use `YYYY-MM-DD`; instants use RFC 3339 with a timezone. Text titles and names are required and limited to 200 UTF-8 bytes.

- Vehicle: `id`, `name`, `make`, `model`, `year`, `odometerMiles`, `createdAt`. Year `0` means unspecified. PATCH accepts any subset of mutable fields. The returned odometer uses the latest measured baseline plus captured Pi distance after that baseline. Saving, editing or deleting a measured odometer record recalculates it. `odometerStatus` distinguishes measured and estimated totals; `odometerExcludedIntervals` warns when overlapping distance was excluded.
- Record: `id`, `vehicleId`, `kind`, `date`, `title`, `notes`, `odometerMiles`, `costCents`, nullable `gallons`. Kinds are `service`, `repair`, `upgrade`, `fuel`, `expense`, `note`, `odometer` and `plan`. An empty date is allowed for notes and plans. Notes are limited to 20,000 UTF-8 bytes. Gallons, when supplied, must be positive and only appear on a fuel record. Notes and plans do not imply an odometer reading; planned costs are estimates, excluded from actual spending.
- Records can include an authentic `recordedAt` RFC3339 capture time whose local date matches `date`. New records receive a server-owned `createdAt` acceptance timestamp. Older date-only records retain their precision. Measured odometer records without a capture time start adding distance at backend acceptance, not midnight; existing date-only baselines use the schema migration boundary. Backdated edits do not invent measurement times. History orders real instants chronologically. Date-only entries keep their supplied calendar day; ambiguous ties use acceptance clamped inside that UTC day, then stable identity. This ordering convention cannot establish an unknown timezone or intraday sequence. Sorting bounds are never displayed as capture times.
- Reminder: `id`, `vehicleId`, `title`, nullable `dueDate`, nullable `dueOdometerMiles`, `completed`. At least one due threshold is required. Optional `recurrence` contains positive `miles`, `months` or `days`, and `fixedIntervals`. Calendar intervals match a date threshold; mileage intervals match a mileage threshold. Completion history remains planned.
- Trip: `id`, `vehicleId`, `title`, `startedAt`, `endedAt`, `distanceMiles`, `points`. Points contain `latitude`, `longitude`, and `recordedAt`, in timestamp order within the trip. A trip is limited to 20,000 points and 31 days. Imported distance is supplied by the caller; PitPilot does not infer a route or an odometer from it.

Automatic GPS trips add server-owned `source:"pi-gps"` and `distanceQuality:"derived"`. Listed routes contain at most 1,000 display points; `recordedPointCount` and `routeSimplified` describe any reduction. Manual creation cannot set automatic provenance. Full canonical fixes remain in private exports. Location responses include actual `recordedAt`, `latitude`, `longitude`, source (`pi-gps` or `smartcar`) and available `speedKph`, `courseDegrees`, `altitudeMeters`, `satellites`, `hdop` and `fixQuality`. Date-only and period-only positions cannot establish capture time and are excluded. Existing generic GPS signal contexts remain valid without native receiver metadata and are not promoted into native trips. An unavailable accuracy estimate is omitted; HDOP is not accuracy in meters. See [GPS recording, boundaries and deletion](gps.md).

Vehicles, records and reminders can contain `tags`, `extraFields` and read-only `source` provenance (`system`, `instance`, `collection`, `id`). Extra fields contain `name`, `value`, `isRequired`, and `fieldType` (text 0, integer 1, decimal 2, date 3, time 4, location 5). Vehicle metadata includes `vin`, `licensePlate`, `notes` and `odometerStatus` (`unknown`, `measured`, `estimated`). Changing vehicle mileage without supplying its status resets provenance to unknown. Records also support `pinned`, `initialOdometerMiles`, `odometerStatus` and `fuel` (`fillToFull`, `missedFill`).

Planned work requires `plan`: `status` (`planned`, `in-progress`, `testing`, `blocked`, `done`), `priority` (`low`, `normal`, `high`, `critical`), `recordKind`, optional `createdAt`, `modifiedAt`, and `reminderIds`. Marking a plan done does not create a service expense. Record and reminder PATCH requests are sparse and preserve unspecified source and future metadata; identity, source and record kind are immutable.

Recurring completion uses `completed: true` with actual `completionDate` and/or `completionOdometerMiles`, plus matching `expectedDueDate` and/or `expectedDueOdometerMiles` from the loaded reminder. An already advanced reminder returns `409`, preventing a retry from skipping another occurrence. Fixed intervals advance once from the prior due value; flexible intervals advance from the supplied completion value. Calendar months clamp to the last valid day. A recurring reminder remains incomplete after advancing. Edit its schedule in a separate PATCH request.

## LubeLogger migration

Both migration routes accept `options` (`source`, `timezone`, `currency`, `distanceUnit`, `fuelUnit`) and `export` (`formatVersion: 1`, `collections`). Apply additionally requires `previewToken`. Use the [CLI workflow](lubelogger-migration.md) to exclude authentication collections before transmission. Bodies remain limited to 4 MiB.

Reports include created, updated, skipped, conflicting and retained counts, `previewToken`, `applied`, and source counts/cost totals. Invalid or unsupported source data returns `422`; stale previews or conflicts return `409` with no partial application. The preview hash binds both source projection and observed target state. Removal at the source retains target records; deleted targets are conflicts. Original domain documents and migration identity mappings are private backup data, not ordinary record-list fields.

## Health and telemetry

Vehicle measurements use a separate [signals API](vehicle-signals.md) for ingestion, latest readings, bounded history, generated-summary conversion and optional authenticated OpenMetrics scraping. Private measurements are not application health telemetry.

Unauthenticated `/healthz` and `/readyz` expose only process and database readiness. The API records route templates, status codes, duration, and trace context. Vehicle identifiers, coordinates, authorization headers, bodies, and query strings do not enter operational logs or spans.

The native client reports `operation`, `durationMs`, and `statusCode` through the authenticated client-events endpoint with the original request's W3C trace context. Operations must match the server's fixed allowlist, duration must be at most 120 seconds, and status must be zero for a transport failure or a valid HTTP status. Transport failures may include `failureKind`: `dns`, `timeout`, `connection`, `tls`, `offline`, or `other`. Freeform errors, URLs, and unknown fields are rejected. The server emits the corresponding client observation span and correlated log, with genuine failures logged at warning level. Cancelled native requests do not emit failure observations. The app has no telemetry ingestion credential. Delivery is best effort with no retry or offline queue, so requests made while disconnected may have no remote client observation.

## Export and backup

Exports contain `schemaVersion: 3`, `exportedAt`, `vehicles`, `records`, `reminders`, `trips`, `importSources`, `importSettings`, `signals`, `signalContexts`, `signalBatches`, `convertedSignalNotes` and additive `odometerBaselines`, `vehiclePhotos`, `automaticTrips`, `gpsHistoryExclusions` and `gpsHistoryPolicies`. Full recorded GPS fixes remain in `signalContexts` even when display routes are simplified; exclusions and policies preserve replay-safe deletions. Import sources retain source identities, original domain JSON and reconciliation hashes. Import settings pin the interpretation first used for each source; mismatched later settings return `409`. Converted notes retain their original records and structured batches, including previous versions when an explicit summary refresh replaces current measurements. Vehicle exports preserve the stored original reading; baselines and intervals reproduce the derived dashboard. The database snapshot is read in one transaction. Protect exported files as personal data. A general JSON restore endpoint is not yet available.

Opening an older database upgrades it to schema 6, adding private vehicle photos, automatic GPS trip projections, deletion policies and versioned conversion archives alongside the odometer calibration ledger, scoped devices and private Smartcar integration state. Earlier servers refuse to open it afterward. To roll back the server version, restore the verified pre-upgrade backup with the service stopped; do not change the schema version pragma to bypass this check. Preserve the separately secured Smartcar encryption key with private backup recovery materials. Portable JSON exports exclude credentials and connection bindings.

For a complete backup, stop the single server instance, copy the entire database directory including any SQLite WAL files, and restart it. Restore the directory with the service stopped and the same file permissions. Test the restored instance before replacing the original. Never run two replicas against the SQLite volume.

Alternatively, `pitpilot backup --database /data/pitpilot.db --output /private/backup.db` creates a consistent online SQLite backup, including committed WAL data. It opens the source read-only without running migrations, checks the output's integrity, and publishes a new `0600` file without overwriting an existing backup. Copy it off the database host. Restore into an empty private directory with the service stopped; do not leave old `-wal` or `-shm` files beside the restored database. Test the restored instance before directing clients to it.
