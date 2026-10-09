# Collector authorization

Collectors use a separate credential bound to one vehicle. They cannot read garage records, pair other devices, control Smartcar, or scrape private vehicle metrics. The household token is never installed on a Pi.

## Pair and manage a device

These routes require the household bearer token:

| Method and path | Behavior |
| --- | --- |
| `POST /api/v1/vehicles/{id}/devices` | Accept `{"name":"Garage Pi"}` and return a device, one-time `enrollmentToken`, and `expiresAt` |
| `GET /api/v1/vehicles/{id}/devices` | List enrollment, revocation, update policy, and last reported status |
| `PATCH /api/v1/devices/{id}` | Set either or both `autoUpdate` and `gpsRecording` boolean policies |
| `DELETE /api/v1/devices/{id}` | Revoke access immediately; preserve observations |

Enrollment expires after 15 minutes. An enrolled or expired token cannot be reused. A vehicle supports up to 16 active or pending devices; revoke abandoned pairings before creating more. Revoking repeatedly is safe. Deleting a vehicle also removes its device authorizations.

The enrollment token is a credential. Save it in a private file and pass the file path to `picollector enroll --token-file`; never place the token in command arguments or shell history. The app only retains it in the active pairing view. If an enrollment response is lost, revoke that device and create another pairing. The server does not retain recoverable plaintext tokens.

## Collector protocol 1

`POST /api/v1/device/enroll` accepts `{"enrollmentToken":"..."}` without household authentication. It returns `deviceId`, `vehicleId`, `autoUpdate`, `protocolVersion:1`, and the collector bearer `token` once. Persist the response privately before collection starts.

The following routes require that collector token:

| Method and path | Behavior |
| --- | --- |
| `GET /api/v1/device/config` | Return paired identity, protocol version, and update policy |
| `POST /api/v1/device/heartbeat` | Report bounded collector status; return 204 |
| `POST /api/v1/device/signals` | Accept a canonical signal batch; return its original `batchId` and ingestion counts |

Heartbeat fields are `version`, `queuedBatches`, `rejectedSamples`, `collectionState`, and `updateState`. Optional `lastObservedAt` and `lastUploadAt` report actual collection and acknowledged PitPilot upload times. The server records `lastSeenAt` itself. A recent heartbeat establishes contact with the collector, not ignition state or available sensor readings.

GPS recording defaults to false. Optional heartbeat `gpsState` is `disabled`, `disconnected`, `waiting_clock`, `waiting_fix`, `fix`, `queue_full` or `paused`. GPS-capable collectors send `X-PitPilot-Capabilities: gps-v1`; only then does the configuration response include `gpsRecording`. Requests without this header retain the original exact configuration shape, allowing older strict decoders and their independent updater to continue working. Enrollment retains its original shape. Cached GPS policy lives in a separate private file, never in the legacy identity document. See [GPS behavior and privacy](gps.md).

Collection states: `starting`, `waiting_clock`, `collecting`, `adapter_unavailable`, `paused`, `queue_full`. Update states: `idle`, `checking`, `downloading`, `staged`, `applying`, `healthy`, `rolled_back`, `failed`, `disabled`.

Use the [native measurement contract](vehicle-signals.md) for signal payloads. The server fixes source to `pi`, derives the vehicle from the credential, and namespaces batch/observation/context identities by device. A caller cannot select another vehicle or impersonate Smartcar. Credential revocation and ingestion are checked in the same database transaction.

Keep a batch immutable in durable local storage until the server returns HTTP200 with its matching batch ID and complete counts. Retrying the same batch skips existing observations. Reusing its identity with conflicting values returns409 and preserves the prior data. Authorization failures return401 and must retain queued data; redirects are not an authorization recovery mechanism.

Only actual measurement timestamps belong in samples. A collector waits for synchronized time instead of substituting upload time for unknown-clock observations. Installer and update behavior are documented in [collector setup](picollector.md).
