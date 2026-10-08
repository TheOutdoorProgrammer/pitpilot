# Native Smartcar

PitPilot connects compatible vehicles through Smartcar and stores timestamped observations directly in its signal history. The iOS app handles consent and vehicle selection. The Go backend keeps credentials private and reconciles data periodically, so opening the app does not wake the vehicle or consume an API request.

This integration uses Smartcar V3 application credentials. An application access token expires after one hour; there are no per-vehicle refresh tokens. Requests for signals carry the connected user's `sc-user-id`. See [Smartcar API authentication](https://smartcar.com/docs/getting-started/how-to/api-authentication).

## Configure the server

Register a Smartcar application, generate API credentials, and enable the read permissions needed for your vehicles. Keep the application UUID separate from the API credential client ID. Register the mobile redirect `sc<application-uuid>://callback` in that application. PitPilot uses [Smartcar Connect](https://smartcar.com/docs/connect/redirect-to-connect) with a random, expiring state and external identifier. It requests vehicle information, odometer, fuel, engine oil, tire pressures, location, and speed. It never requests vehicle controls. Speed uses the documented [`read_speedometer` permission](https://smartcar.com/docs/api-reference/permissions).

The backend reads credentials from mounted files:

| Environment variable | Purpose |
| --- | --- |
| `PITPILOT_SMARTCAR_APPLICATION_ID` | Application UUID used by Connect |
| `PITPILOT_SMARTCAR_CLIENT_ID_FILE` | File containing the V3 API credential client ID |
| `PITPILOT_SMARTCAR_CLIENT_SECRET_FILE` | File containing the API credential secret |
| `PITPILOT_SMARTCAR_ENCRYPTION_KEY_FILE` | File containing a base64-encoded random 32-byte encryption key |
| `PITPILOT_SMARTCAR_MODE` | `live` or `simulated`; defaults to `live` |
| `PITPILOT_SMARTCAR_POLL_INTERVAL` | Go duration between successful reconciliations; defaults to `1h`, allowed `5m` through `24h` |

Use your deployment's secret manager to mount these files. Do not put them in Git, the app bundle, URLs, logs, or ordinary garage exports. Access tokens are cached only in memory. Encrypted connection identities and temporary authorization sessions are stored in SQLite using AES-256-GCM with random nonces and row-specific associated data. The encryption key is external to the database.

Back up the stopped database, or use a consistent SQLite backup, together with the separately secured encryption key. Ordinary garage JSON export includes recorded signals but intentionally excludes Smartcar identities and authorization sessions. JSON import is not implemented; use the database backup for recovery. Database restore with a different encryption key or application configuration fails closed. Key rotation requires decrypting and re-encrypting existing bindings with both keys available; simply replacing the key file is not a rotation procedure.

## Connect, reconnect, and detach

The native app starts an `ASWebAuthenticationSession` using the server-supplied authorization URL and application-specific callback scheme. OEM credentials stay on Smartcar's consent screens. The callback returns identifiers and state to the app, which posts them to the authenticated backend. The server validates state, expiry, application ownership, and the external identifier before presenting available vehicles. The user explicitly selects which remote vehicle belongs to the selected PitPilot vehicle. One remote vehicle cannot be paired with two PitPilot vehicles.

Reconnect uses the [dedicated reauthentication flow](https://smartcar.com/docs/connect/re-auth/redirect-to-connect). Cancelling or failing reauthentication keeps the existing local connection. A successful reconnect must still pass provider verification before replacing it.

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

Reconciliation uses durable leases and retry deadlines. Manual sync is queued and cannot bypass OEM throttling. HTTP 401 permits one application-token reacquisition, while 429 honors `Retry-After` and introduces persisted backoff. Application rate limits also defer other vehicle jobs. New grants may remain in provisioning while the OEM prepares data. A stale or missing signal is not proof that consent was revoked. [Smartcar rate-limit documentation](https://smartcar.com/docs/errors/api-errors/v3/rate-limit-errors).

REST polling retrieves available current state. It cannot reconstruct every route or a history of changes between polls. Parked locations do not describe a driven route. This deployment does not need a public webhook endpoint. Webhooks can be added later with signature verification, durable event deduplication, and reconciliation for missed deliveries; they are not silently implied by periodic sync.

Operational traces and logs contain route templates, fixed state/error categories, counts, and timings. They exclude vehicle values, coordinates, VINs, provider identities, authorization URLs, tokens, callback parameters, and provider error bodies. The existing authenticated vehicle metrics endpoint remains private and opt-in.

## HTTP interface

All routes require the household API token. POST requests require JSON. Neither Connect callbacks nor provider identities grant access to PitPilot by themselves.

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
