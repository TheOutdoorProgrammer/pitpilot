# PitPilot for iOS

A native garage journal for a self-hosted PitPilot server. This initial release supports vehicles, service and fuel records, due-date and mileage reminders, and maps of trips already uploaded to the server. It requires iOS 17 or newer.

## Build and test

Install Xcode and XcodeGen, then run from this directory:

```sh
xcodegen generate
bash ../scripts/test-ios.sh
```

The Xcode project and Info.plist are generated from `project.yml`. Release automation supplies signing, the build number, and optionally `PITPILOT_BASE_URL` to prefill the connection screen. No API token or telemetry ingestion credential belongs in build settings or the application bundle.

Both GitHub iOS jobs use the repository's self-hosted macOS runner. The test helper needs jq and an installed runtime supporting iPhone 17. It prepares accessibility on a disposable simulator, runs the full suite, and removes only that simulator on exit.

The instrument-dial app icon is reproducible:

```sh
swift scripts/generate-icon.swift PitPilot/Assets.xcassets/AppIcon.appiconset/AppIcon.png
```

## Connection and privacy

Enter the server's HTTPS origin and API token. URLs containing paths, embedded credentials, queries, and fragments are rejected. HTTP redirects are not followed. The connection is saved in device-only Keychain storage while the device is unlocked.

The app caches loaded vehicles, records, reminders, and trips in a file protected by iOS Data Protection and excluded from backups. Previously loaded information can be read offline; writes require connectivity. Disconnecting deletes the local cache and token, leaving server data intact.

Trip maps display recorded samples. Samples over five minutes apart are disconnected, and invalid coordinates are omitted. Map tiles may require internet access. PitPilot does not request location permission or record phone location in this release.

Network operations create W3C trace context for server correlation and emit bounded local structured logs containing method, status, and trace/span IDs. A separate authenticated request relays only an allowlisted operation name, duration, and HTTP status to the backend's client-events endpoint for Grafana traces and correlated logs. Delivery failures never block the user, and events are not queued or retried. Logs and events exclude addresses, tokens, vehicle details, form values, and coordinates. General OTLP credentials must never be placed in the app.

## Release boundaries

- Reminders are visible in the app; push and local notification scheduling are not implemented.
- Automatic Raspberry Pi and Smartcar ingestion, Pi updates, receipt attachments, LubeLogger migration, and CrewChief AI remain checklist work.
- Costs use USD, odometers use miles, and fuel quantities use US gallons in this release.
- UI tests activate a transport stub only in Debug builds with `--ui-testing`. They use separate test Keychain/cache entries. Release builds contain no fixture transport.
