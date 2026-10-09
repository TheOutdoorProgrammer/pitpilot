# Recover retained receiver history

`pitpilot recover-receiver` recovers actual OBD events retained by the legacy Grinch receiver. It reads a **closed bbolt backup** without modifying it. Keep that original backup independently: the importer stores its SHA-256 and each original event, but the event archive is not a replacement for the entire source database.

Recovery needs an existing PitPilot vehicle. First take a consistent PitPilot backup and rehearse against a copy. The server uses SQLite schema 7; returning to an older server requires restoring the pre-upgrade database, not opening schema 7 with old binaries.

```sh
pitpilot recover-receiver --input receiver-closed.db \
  --vehicle VEHICLE_ID --database rehearsal.db

pitpilot recover-receiver --input receiver-closed.db \
  --vehicle VEHICLE_ID --database rehearsal.db --apply PREVIEW_TOKEN
```

For a deployed server, replace `--database rehearsal.db` with `--server https://pitpilot.example.com --token-file /private/api-token`. The token stays in its file. Both modes preview by default. Apply requires the exact returned preview token, which binds the source, target vehicle and retained Pi/archive state. If the collector uploads between preview and apply, preview again. After a network failure, preview again to discover whether the transaction committed.

Recovery is atomic and rejects malformed events, mismatched source identities, conflicting readings, changed archives and missing previously recovered rows. Repeated source events with identical content are counted once. A fresh preview and apply after a completed import reports skipped events and creates no measurements.

Each timestamped reading uses the native Pi mapper, canonical units, measured quality and its original timestamp including nanoseconds. Exact timestamp/metric/semantic matches reuse existing Pi rows without changing their keys. A one-nanosecond difference remains a separate observation. A conflicting value at the exact same timestamp aborts instead of guessing. Existing LubeLogger summaries and source archives remain unchanged, including their lower timestamp precision.

Events with no original wall-clock timestamp are archived without generating chart points or diagnostic timestamps. Recovery never estimates their dates from boot uptime, and does not generate distance, odometer readings, trips or GPS locations. Missing calendar days and complete physical-trip coverage cannot be inferred from retained events.

The authenticated API exposes `POST /api/v1/migrations/receiver/preview` and `/apply`. Requests contain `vehicleId`, `snapshot` (`sha256`, `deviceId`, raw `events`) and optional `previewToken`. Only these routes accept up to 32 MiB; other request limits are unchanged. The CLI additionally bounds the source to 256 MiB, 100,000 events and 64 KiB per event, validates bbolt integrity and checks its hash before and after reading. An active bbolt writer is rejected.

`GET /api/v1/export` includes `receiverEventArchives`, containing original event JSON, backup provenance and the exact projection keys used for reconciliation. The additive export field retains export format version 3. Store exports and backups privately because vehicle history is sensitive.

Operational logs and traces report only fixed operation names, errors and counts. Raw events, vehicle/device identifiers, paths, credentials and signal values are not included in telemetry.
