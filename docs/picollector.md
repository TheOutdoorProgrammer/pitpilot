# Native Pi collector

`picollector` connects a supported OBD adapter to PitPilot without granting the Pi access to the rest of the household garage. It runs directly on Linux and keeps immutable deliveries in a private bbolt queue while offline. Quill builds release binaries with GoReleaser; the Pi never builds Go code or runs a build container.

The first adapter profile supports an ELM327-compatible serial/RFCOMM adapter on J1850 VPW, with engine ECU source `0x10`. It reuses the tested read-only reader, PID discovery, engine filtering, diagnostic validation and cancellation handling from the existing collector. It does not send diagnostic clear commands. Other protocols require another tested profile. Bluetooth pairing and RFCOMM binding are prerequisites, not automatic actions performed by this installer.

The collector preserves actual observations, including the distinction between unknown diagnostics and a confirmed empty fault list. It does not manufacture fuel purchases, completed maintenance, true odometer values or GPS routes. Adapter voltage is vehicle supply voltage, not the Pi's regulated power rail. No software poweroff, kernel update, network reconfiguration or reboot is part of this service.

## Installation and enrollment

Use the published Linux arm64 executable on a 64-bit Pi, or amd64 on an appropriate Linux host. Confirm architecture and verify the release artifact against its signed update manifest using the pinned public key through a trusted bootstrap environment. The installer accepts a locally verified executable and does not download or execute an unverified bootstrap command.

Run `sudo bash scripts/picollector-install.sh /path/to/picollector`. It creates a dedicated account, private state directory and systemd units. It does not start collection or enable updates. Place the adapted example configuration at `/etc/pitpilot/collector.json`, owned by root and readable only by the collector group (`0640`). Keep the standard state directory `/var/lib/pitpilot-collector`. The configured clock marker must indicate synchronization during the current boot; the supplied path is systemd-timesyncd's marker.

Create a device in the vehicle's Pi settings. Save its one-use enrollment token to a root-owned `0600` file, then run:

```sh
sudo /opt/pitpilot/picollector/current enroll \
  --config /etc/pitpilot/collector.json \
  --token-file /root/pitpilot-enrollment
```

Root enrollment writes `identity.json` atomically with the dedicated collector account's ownership and mode `0600`. The token is never passed as an argument value or printed. Remove the enrollment token file after success. A consumed enrollment whose response was lost must be revoked and replaced through the app. Existing identity files are never overwritten. Never re-enroll into a different device's queue directory.

Complete the exclusive adapter handoff described below, then enable `picollector.service` and `picollector-update.timer` with systemd. The updater uses a distinct root service with fixed unit/install paths. The reader is unprivileged, belongs to `dialout`, and cannot modify executables or update trust state.

## Delivery and time

Each sample is stored as one immutable canonical signal batch. Stable batch/observation keys survive restarts and retries. An upload removes its pending marker only after a strict acknowledgement confirms the exact batch ID and all submitted observations/contexts. Transactional persistence on the server precedes acknowledgement. Lost responses cause safe duplicate retries. Exponential backoff with jitter and bounded server retry hints avoids a tight failure loop. Redirects are refused for authenticated requests.

Sampling waits for a synchronized clock. A clock step during an OBD read rejects that timestamp. This leaves an explicitly unsampled boot interval, rather than presenting upload time as driving time. Status distinguishes waiting for clock, adapter unavailability, collection, revocation and a full queue. Capacity preserves older deliveries and records a durable rejected-sample counter when new samples cannot fit. Revocation pauses new collection and retains queued data.

The queue owns its own device identity, monotonic sequence and schema marker. It is not compatible with another collector's live database. Never point this service at an existing Sonoma queue or copy a live bbolt file without its supported snapshot procedure.

## Keeping an existing receiver

An optional `legacy` object preserves an existing receiver feed during migration:

```json
{
  "endpoint": "https://receiver.example.com/v1/obd/events",
  "deviceId": "existing-device",
  "tokenFile": "/etc/pitpilot/legacy-token"
}
```

Add this object to the collector configuration before first startup. The receiver's separate scoped token must be a private regular file readable by the collector account. It is independent of the PitPilot device token. This sink supports the existing version-1 OBD event contract and exact accepted-ID acknowledgement.

One bbolt transaction stores both the PitPilot batch and legacy envelope. Separate durable pending indexes let either destination advance while the other is offline. The record is removed only after both required destinations acknowledge it. Legacy configuration cannot be silently disabled or rebound for an existing queue. The legacy device ID stays unchanged; the native reader starts a fresh random collector session and assigns durable sequence numbers within its new queue. Session boundaries prevent inferred continuity across the handoff.

For an existing vehicle, stop the old reader first, preserve its binary/configuration/identity/queue, and drain its pending deliveries with its upload-only mode. Verify that it is empty before starting the native reader. Preserve the existing adapter binding, receiver integrations and physical power policy. Verify actual PitPilot acknowledgement and actual downstream receiver delivery before disabling the old service permanently. Never run both readers against one adapter.

Rollback stops the native reader first and preserves its state directory, then restores the old reader. `picollector drain --config /etc/pitpilot/collector.json`, run as the collector account while its service is stopped, finishes retained native deliveries without opening the adapter. Neither rollback path resets sequence numbers or restores an older queue snapshot over newly collected data. Live handoff and physical power-loss behavior require deployment acceptance; unit fixtures do not prove those operations happened.

## Signed automatic updates

Release manifests are `picollector_linux_arm64.update.json` and `picollector_linux_amd64.update.json`. Each wraps base64 payload bytes and an Ed25519 signature. The embedded release public key authenticates the exact payload, including platform, stable semantic version, monotonic sequence, immutable release URL, SHA-256, size and expiry. Sequence is `major * 10^12 + minor * 10^6 + patch`, with each component below one million. Unsigned development snapshots cannot be installed automatically.

The updater checks fresh device policy before downloading and again before activation. Disabling automatic updates in the app leaves the current collector running. A missing trusted clock, expired metadata, revoked credentials, wrong platform, altered artifact or older sequence refuses the update and preserves collection. Metadata expires after 90 days; a stale latest manifest needs a newly signed release before automatic updates resume.

Verified binaries are staged in root-owned version directories. The updater fsyncs a recovery journal and sequence/digest watermark before atomically changing the current executable symlink. Queue, credentials and configuration live elsewhere. A successful upgrade requires the systemd main PID to report the expected version and fresh queue readiness through a stable observation window. It does not require a running engine or Internet connectivity to prove local health.

Failed startup restores the prior trusted executable and checks its health. The update service invokes the separately installed, root-owned `/opt/pitpilot/picollector/updater`, not the mutable `current` reader. It remains executable even when a candidate cannot start. An interrupted activation is recovered before any network access at the next updater run. The durable journal preserves the actual last-good reader version across multiple upgrades; it does not assume that version matches the older bootstrap updater.

Automatic releases update the reader only. Security fixes to the updater itself or replacement of its pinned signing key require a verified, operator-managed replacement of the independent updater while its timer/service are stopped. Preserve the old updater for recovery and never replace it with an unverified current candidate. Local health rollback does not lower the signed metadata watermark, and a known-bad target is not retried automatically. Queue format changes must remain readable by the rollback binary; destructive queue migrations are not supported by this updater. This single pinned-key design does not implement TUF threshold roles or independent online/offline metadata keys.

## Operational telemetry

Structured logs and OpenTelemetry trace boundaries cover adapter reads, durable intake, authenticated delivery and update operations. `service.name` is `picollector`. Configure a private, device-restricted OTLP proxy through root-owned `/etc/pitpilot/telemetry.env`; never install general Grafana Cloud ingestion credentials on a vehicle device. The systemd reader can use credentials supplied by the service manager without reading that root-only file. Logs omit raw replies, vehicle/device identifiers, coordinates, configuration URLs and tokens. Verify actual delivered traces and correlated logs during deployment acceptance.

See [third-party notices](../deploy/picollector/THIRD-PARTY-NOTICES.txt) for reused dependency licenses.
