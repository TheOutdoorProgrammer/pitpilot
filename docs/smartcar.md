# Native Smartcar

PitPilot connects compatible vehicles through Smartcar and stores timestamped observations directly in its signal history. The iOS app handles consent and vehicle selection. The Go backend keeps credentials private and reconciles data periodically, so opening the app does not wake the vehicle or consume an API request.

Smartcar locations also update the vehicle's last-known map pin without requiring a Pi or any trips. The map identifies Smartcar as the source and preserves the OEM observation time and `CURRENT` or `LAST_PARKED` location type when supplied. A missing or unavailable update leaves the last valid position visible with its original timestamp. Smartcar positions do not create trip routes; Pi-equipped vehicles retain their independent GPS maps and recorded trips. See the [location contract](gps.md).

This integration uses Smartcar V3 application credentials. An application access token expires after one hour; there are no per-vehicle refresh tokens. Requests for signals carry the connected user's `sc-user-id`. See [Smartcar API authentication](https://smartcar.com/docs/getting-started/how-to/api-authentication).

## Configure the server

Register a Smartcar application, generate API credentials, and enable the read permissions needed for your vehicles. Keep the application UUID separate from the API credential client ID. Register the mobile redirect `sc<application-uuid>://callback` in that application. PitPilot uses [Smartcar Connect](https://smartcar.com/docs/connect/redirect-to-connect) with a random, expiring state and external identifier. It requests vehicle information, odometer, fuel, engine oil, tire pressures, location, and speed. It never requests vehicle controls. Speed uses the documented [`read_speedometer` permission](https://smartcar.com/docs/api-reference/permissions).

The backend reads credentials from mounted files:

| Environment variable | Purpose |
| --- | --- |
| `PITPILOT_SMARTCAR_ENABLED` | Optional explicit `false` disables the integration while retaining history and private bindings; otherwise complete configuration enables it |
| `PITPILOT_SMARTCAR_APPLICATION_ID` | Application UUID used by Connect |
| `PITPILOT_SMARTCAR_CLIENT_ID_FILE` | File containing the V3 API credential client ID |
| `PITPILOT_SMARTCAR_CLIENT_SECRET_FILE` | File containing the API credential secret |
| `PITPILOT_SMARTCAR_ENCRYPTION_KEY_FILE` | File containing a base64-encoded random 32-byte encryption key |
| `PITPILOT_SMARTCAR_MODE` | `live` or `simulated`; defaults to `live` |
| `PITPILOT_SMARTCAR_POLL_INTERVAL` | Minimum Go duration between reconciliations; defaults to `1h`, allowed `1h` through `24h` |

Use your deployment's secret manager to mount these files. Do not put them in Git, the app bundle, URLs, logs, or ordinary garage exports. Access tokens are cached only in memory. Encrypted connection identities and temporary authorization sessions are stored in SQLite using AES-256-GCM with random nonces and row-specific associated data. The encryption key is external to the database.

Back up the stopped database, or use a consistent SQLite backup, together with the separately secured encryption key. Ordinary garage JSON export includes recorded signals but intentionally excludes Smartcar identities and authorization sessions. JSON import is not implemented; use the database backup for recovery. Database restore with a different encryption key or application configuration fails closed. Key rotation requires decrypting and re-encrypting existing bindings with both keys available; simply replacing the key file is not a rotation procedure.

## Connect, reconnect, and detach

The native app starts an `ASWebAuthenticationSession` using the server-supplied authorization URL and application-specific callback scheme. OEM credentials stay on Smartcar's consent screens. The callback returns identifiers and state to the app, which posts them to the authenticated backend. The server validates state, expiry, application ownership, and the external identifier before presenting available vehicles. The user explicitly selects which remote vehicle belongs to the selected PitPilot vehicle. One remote vehicle cannot be paired with two PitPilot vehicles.

Reconnect uses the [dedicated reauthentication flow](https://smartcar.com/docs/connect/re-auth/redirect-to-connect). Cancelling or failing reauthentication keeps the existing local connection. A successful reconnect must still pass provider verification before replacing it.

After Connect or reauthentication, cached signal authentication errors timestamped before the new consent session began leave the connection awaiting provider data. Unknown or newer authentication failures still show a reconnect warning. Both cases continue checking on the hourly schedule so subscription renewal or provider recovery can restore collection without another consent loop. Existing connections created before authorization timestamps were recorded also resume hourly checks; their old errors cannot safely be classified as pre-authorization.

Disconnect in PitPilot stops its jobs and removes its local binding and pending sessions. It preserves collected history and does not revoke the provider grant. This matters when another service shares the grant. Revoke access in the provider's account controls when you intend to stop every consumer of that grant.

An existing application-owned grant can be adopted without new consent through the household-authenticated adoption endpoint. This is an administrator operation: pass the existing Smartcar user ID, inspect the returned candidate vehicles, and bind an opaque candidate ID. The server verifies that the grant belongs to its configured application. It does not refresh legacy tokens, edit another collector, or revoke anything.

## Data and scheduling semantics

The supported signal mappings are:

| Smartcar signal | PitPilot metric or context |
| --- | --- |
| `internalcombustionengine-fuellevel` | `fuel_level_pct`, percent |
| `internalcombustionengine-amountremaining` | `fuel_remaining_l`, liters |
| `internalcombustionengine-oillife` | `oil_life_pct`, remaining useful life |
| `internalcombustionengine-range` | `range_km`, estimated |
| `odometer-traveleddistance` | `odometer_km` |
| `motion-currentspeed` | `speed_kph` |
| `wheel-tires` | `tire_fl_kpa`, `tire_fr_kpa`, `tire_rl_kpa`, `tire_rr_kpa` for explicit 2x2 wheel layouts |
| `location-preciselocation` | Private location context preserving `CURRENT` or `LAST_PARKED` |

Values use the provider's OEM update time, not the HTTP retrieval time. Missing timestamps, missing values, invalid units, and failed signals are counted as unavailable. They do not become zero or receive a fabricated observation time. Unsupported signals are counted separately. Fuel level zero and coordinates `(0, 0)` are valid only when explicitly present. Repeated snapshots deduplicate by vehicle, signal, component, and OEM timestamp; old observations can fill history without replacing the latest point. Conflicting values for the same identity stop that batch and appear as `observation_conflict`.

These mappings reuse protocol knowledge from the owner's existing collector while using separate validation, storage, and scheduling. Provider documentation: [engine signals](https://smartcar.com/docs/api-reference/signals/internalcombustionengine), [wheel signals](https://smartcar.com/docs/api-reference/signals/wheel), [location signals](https://smartcar.com/docs/api-reference/signals/location), and [REST signal metadata](https://smartcar.com/docs/api-reference/list-signals).

Each integration is checked at most once per hour by default, including reconnect warnings and temporary errors. The first check for a new binding can run immediately. Manual sync queues a check without moving an existing deadline earlier. Attempts are persisted before contacting the provider, so an expired worker lease or process restart cannot trigger another check within the hour. Reauthentication preserves the previous attempt and provider backoff. HTTP 401 permits one application-token reacquisition within a check; paginated responses may require several HTTP requests, so a check is not a promise of exactly one API request. HTTP 429 honors `Retry-After` when longer than the configured interval, and application rate limits also defer other vehicle jobs. New grants may remain in provisioning while the OEM prepares data. A stale or missing signal is not proof that consent was revoked. [Smartcar rate-limit documentation](https://smartcar.com/docs/errors/api-errors/v3/rate-limit-errors).

The status includes the next scheduled check even when reconnect is recommended. A successful check timestamp is recorded only when usable, timestamped vehicle data is received; an HTTP 200 containing only failed signals does not count. Smartcar's own vehicle refresh cadence is independent of PitPilot's schedule. Repeated cached readings keep their original OEM measurement time.

REST polling retrieves available current state. It cannot reconstruct every route or a history of changes between polls. Parked locations do not describe a driven route. Optional signed webhooks deliver available updates directly, with hourly polling retained for recovery.

Operational traces and logs contain route templates, fixed state/error categories, counts, and timings. They exclude vehicle values, coordinates, VINs, provider identities, authorization URLs, tokens, callback parameters, and provider error bodies. The existing authenticated vehicle metrics endpoint remains private and opt-in.

## HTTP interface

The household routes below require the API token. POST requests require JSON. Neither Connect callbacks nor provider identities grant access to PitPilot by themselves. The separate webhook route uses provider signature authentication.

| Method and path | Behavior |
| --- | --- |
| `GET /api/v1/integrations/smartcar` | Configuration availability and mode |
| `GET /api/v1/vehicles/{id}/smartcar` | Connection state, sync timestamps, counts, retry time |
| `POST /api/v1/vehicles/{id}/smartcar/sessions` | Start with `{"intent":"connect"}` or `{"intent":"reconnect"}` |
| `GET /api/v1/vehicles/{id}/smartcar/sessions/{session}` | Read session state and candidate list |
| `POST /api/v1/vehicles/{id}/smartcar/sessions/{session}/complete` | Submit `state` and callback `userId` or `vehicleId`; optional `externalId` or `error` |
| `POST /api/v1/vehicles/{id}/smartcar/sessions/{session}/bind` | Select `{"candidateId":"<opaque candidate>"}` |
| `POST /api/v1/vehicles/{id}/smartcar/adoptions` | Verify an existing grant with `{"userId":"<existing provider user>"}` and return candidates |
| `POST /api/v1/vehicles/{id}/smartcar/sync` | Queue a bounded sync with `{}` |
| `DELETE /api/v1/vehicles/{id}/smartcar` | Detach locally, retaining collected history |

The session response includes `sessionId`, `state`, `expiresAt`, and `candidates`; a new consent session also returns `authorizationUrl` and `callbackScheme`. Candidate objects contain `candidateId`, `make`, `model`, and `year`. Smartcar's callback uses snake case (`user_id`, `vehicle_id`, `external_id`), while the completion JSON uses camel case. Reject duplicate callback keys before submitting them.

A validated callback declining consent consumes the session and returns HTTP 200 with `state: "cancelled"` and an empty candidate list. Other provider callback failures return a fixed safe error without exposing the provider's message. Neither case changes an existing binding.

## Webhook delivery

Enable Smartcar's V4 webhook payloads with these additional server settings:

| Variable | Value |
| --- | --- |
| `PITPILOT_SMARTCAR_MANAGEMENT_TOKEN_FILE` | Mounted file containing the Application Management Token |
| `PITPILOT_SMARTCAR_WEBHOOK_ID` | UUID of the webhook created in the Smartcar dashboard |

Both settings are required together. With neither configured the receiver returns 503 and polling continues. The Application Management Token is separate from the OAuth client secret and must stay in server-side secret storage. Do not send it to the iOS app or put it in a callback URL.

Create a webhook in the [Smartcar dashboard](https://dashboard.smartcar.com/integrations), using the supported signals listed above and `POST /api/v1/integrations/smartcar/webhook` on a public HTTPS hostname. Expose only that exact path and method, leaving the household API private. Keep automatic subscription off until verification succeeds. Save the webhook UUID and management token, configure the server, then use Smartcar's Verify action. Subscribe the already-connected vehicle after verification. Current V3 subscription management uses `POST https://management.api.smartcar.com/v3/subscriptions` with `data.attributes.webhookId`, `userId`, and `vehicleId`; creating an integration does not itself establish successful vehicle delivery.

The unsigned `VERIFY` challenge returns its HMAC-SHA256 using the management token. Challenges are restricted to bounded token characters to prevent the challenge route from signing arbitrary JSON events. Vehicle events require a constant-time check of `SC-Signature` against the unchanged raw body, a matching webhook UUID and mode, and both provider user and vehicle matching the encrypted local binding. Bodies are capped at 1 MiB and reject duplicate JSON keys. Unbound vehicles are acknowledged without ingestion.

`VEHICLE_STATE` reuses the existing signal adapter, preserving OEM timestamps, units, provenance, deduplication and independent last-known locations. Schema 9 stores a hashed event receipt in the same transaction as readings and status. Delivery retries may change their delivery metadata but cannot change an event's contents. Database failures return 503 without acknowledging the event. A concurrent polling job cannot erase a newer webhook observation. Older measurements can fill history while the newest valid position remains on the map.

`VEHICLE_ERROR` records delivery metadata and the count of active errors. Error and resolution events have no trustworthy error-onset timestamp and never create readings, mark a successful sync, erase good data, or force another consent cycle. Polling confirms account recovery. Status exposes `lastWebhookAt`, `lastWebhookEventType`, and `webhookErrors`; these describe the last stored delivery, not live tracking or proof of fresh OEM data. Configuration exposes `webhooksConfigured`.

Take a consistent database backup before upgrading to schema 9. An older binary refuses schema 9; stop the server and restore the verified backup for rollback, together with the existing encryption key. Ordinary JSON exports omit private integration bindings and webhook receipts. Retain the management token securely for webhook verification after restoring.

Provider contracts: [payload verification](https://smartcar.com/docs/integrations/webhooks/payload-verification), [callback verification](https://smartcar.com/docs/integrations/webhooks/callback-verification), [vehicle state](https://smartcar.com/docs/api-reference/webhooks/events/vehicle-state), [vehicle errors](https://smartcar.com/docs/api-reference/webhooks/events/vehicle-error). See [ADR 11](../adr/0011-receive-signed-smartcar-webhooks-alongside-bounded-polling.md) for the delivery and recovery trade-offs.
