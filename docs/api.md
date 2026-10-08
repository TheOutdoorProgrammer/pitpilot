# API v1

All `/api/v1` requests require `Authorization: Bearer <household-token>`. Use HTTPS. Keep the token in the iOS Keychain or an integration's secret store. Tokens are not user accounts: a token grants access to the entire household. Rotate the deployment secret and restart the server to revoke it.

Requests with bodies use `Content-Type: application/json`. Responses are JSON, except successful deletes (`204`). Create returns `201` with the saved resource. Errors use `{"error":"message"}` and an appropriate HTTP status. Unknown fields, multiple JSON documents, invalid units, negative costs, and bodies above 4 MiB are rejected. Client-supplied IDs on creation are replaced by server-generated IDs.

| Method | Path | Result |
| --- | --- | --- |
| GET, POST | `/api/v1/vehicles` | List or create vehicles |
| GET, PATCH, DELETE | `/api/v1/vehicles/{id}` | Read, edit, or delete a vehicle and its records |
| GET, POST | `/api/v1/vehicles/{id}/records` | List or create records |
| DELETE | `/api/v1/records/{id}` | Delete a record |
| GET, POST | `/api/v1/vehicles/{id}/reminders` | List or create reminders |
| PATCH, DELETE | `/api/v1/reminders/{id}` | Set `completed` or delete a reminder |
| GET, POST | `/api/v1/vehicles/{id}/trips` | List or create recorded trips |
| DELETE | `/api/v1/trips/{id}` | Delete a trip and its recorded points |
| GET | `/api/v1/export` | Download a consistent JSON snapshot of household records |
| POST | `/api/v1/client-events` | Relay a bounded native request observation to server telemetry |

List responses are arrays. Missing resources return `404`; an existing resource with no children returns `[]`. Lists currently return the full household history. Pagination and idempotency keys are not implemented; clients must not automatically retry create requests after an ambiguous network failure.

## Resource fields

All IDs are opaque strings. Mileage is explicitly in miles, liquid volume in US gallons, and costs are integer cents. Currency conversion and multiple currencies are not implemented. Calendar dates use `YYYY-MM-DD`; instants use RFC 3339 with a timezone. Text titles and names are required and limited to 200 UTF-8 bytes.

- Vehicle: `id`, `name`, `make`, `model`, `year`, `odometerMiles`, `createdAt`. Year `0` means unspecified. PATCH accepts any subset of mutable fields. Mileage corrections are explicit; historical records do not silently change the dashboard mileage.
- Record: `id`, `vehicleId`, `kind`, `date`, `title`, `notes`, `odometerMiles`, `costCents`, nullable `gallons`. Kinds are `service`, `repair`, `upgrade`, `fuel`, `expense`, and `note`. Notes are limited to 20,000 UTF-8 bytes. Gallons, when supplied, must be positive and only appear on a fuel record.
- Reminder: `id`, `vehicleId`, `title`, nullable `dueDate`, nullable `dueOdometerMiles`, `completed`. At least one due threshold is required. Completion currently records a boolean; recurring schedules and completion history remain planned.
- Trip: `id`, `vehicleId`, `title`, `startedAt`, `endedAt`, `distanceMiles`, `points`. Points contain `latitude`, `longitude`, and `recordedAt`, in timestamp order within the trip. A trip is limited to 20,000 points and 31 days. Imported distance is supplied by the caller; PitPilot does not infer a route or an odometer from it.

## Health and telemetry

Unauthenticated `/healthz` and `/readyz` expose only process and database readiness. The API records route templates, status codes, duration, and trace context. Vehicle identifiers, coordinates, authorization headers, bodies, and query strings do not enter operational logs or spans.

The native client reports `operation`, `durationMs`, and `statusCode` through the authenticated client-events endpoint with the original request's W3C trace context. Operations must match the server's fixed allowlist, duration must be at most 120 seconds, and status must be zero for a transport failure or a valid HTTP status. Unknown fields are rejected. The server emits the corresponding client observation span and correlated log; the app has no telemetry ingestion credential. Delivery is best effort with no retry or offline queue, so requests made while disconnected may have no remote client observation.

## Export and backup

Exports contain `schemaVersion`, `exportedAt`, `vehicles`, `records`, `reminders`, and `trips`. The database snapshot is read in one transaction. Protect exported files as personal data. A JSON restore/import endpoint is not yet available.

For a complete backup, stop the single server instance, copy the entire database directory including any SQLite WAL files, and restart it. Restore the directory with the service stopped and the same file permissions. Test the restored instance before replacing the original. Never run two replicas against the SQLite volume.
