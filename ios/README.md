# PitPilot for iOS

A native garage journal for a self-hosted PitPilot server. It supports a vehicle signal dashboard with history charts, maintenance history, notes, odometer readings, planned work, recurring reminders, and maps of trips already uploaded to the server. It requires iOS 17 or newer.

## Build and test

Install Xcode and XcodeGen, then run from this directory:

```sh
xcodegen generate
bash ../scripts/test-ios.sh
```

The Xcode project and Info.plist are generated from `project.yml`. Release automation supplies signing, the build number, and optionally `PITPILOT_BASE_URL` to prefill the connection screen. No API token or telemetry ingestion credential belongs in build settings or the application bundle.

Both GitHub iOS jobs use the repository's self-hosted macOS runner. The test helper needs jq, Ruby, and an installed runtime supporting iPhone 17. Each run has isolated build files and result artifacts. It builds the test targets before booting a disposable simulator, prepares accessibility, runs the full suite, and removes its simulator and build files on exit. Cancellation stops the active child process group.

Before building, the helper can recover an abandoned helper from this checkout only when process ownership, simulator identity, and a completed GitHub run all agree. Unverifiable candidates are left alone; build isolation still applies. The recovery lookup uses GitHub's public API without credentials. `ruby scripts/test-ios-runner-test.rb` checks these guards and cancellation with harmless shell fixtures; it requires Minitest and runs in both native workflows.

The instrument-dial app icon is reproducible:

```sh
swift scripts/generate-icon.swift PitPilot/Assets.xcassets/AppIcon.appiconset/AppIcon.png
```

## Connection and privacy

Enter the server's HTTPS origin and API token. URLs containing paths, embedded credentials, queries, and fragments are rejected. HTTP redirects are not followed. The connection is saved in device-only Keychain storage while the device is unlocked.

The app caches loaded vehicles, records, reminders, trips, last known locations, signal cards, and requested signal history in a file protected by iOS Data Protection and excluded from backups. Vehicle photos use a separate protected cache bound to the server, credential, vehicle, and photo revision. Previously loaded information can be read offline; writes require connectivity. Disconnecting deletes these local caches and the token, leaving server data intact.

Trip maps display recorded samples. Native Pi GPS samples over two minutes apart are disconnected; older trip sources retain the five-minute drawing limit. Invalid coordinates are omitted. Simplified routes identify the retained sample count and may show gaps instead of inventing continuity. GPS-derived distance stays separate from the odometer. Trips load 50 at a time, with older trips available on demand. Last known location always includes its capture time and becomes stale after 15 minutes. Map tiles may require internet access. PitPilot does not request location permission or record phone location.

Open the hamburger menu for Settings and vehicle integrations. The vehicle menu also contains the editor and a confirmed deletion action for native GPS history. Enable Record GPS trips for a paired Pi to collect locations from a supported USB GPS receiver; missing GPS does not imply the engine collector is disconnected. Turning recording off leaves saved history intact. Deleting location history removes saved native fixes and automatic trips, while new fixes can still arrive if recording remains enabled.

Deleting native GPS data also removes its cached coordinates from Collection details and rejects requests started before deletion. The app refreshes the last known location afterward; a surviving Smartcar position can still appear with its own capture time and source. A failed refresh keeps an already saved Smartcar position instead of claiming that the vehicle has no known location.

The vehicle editor supports VIN, license plate, and a photo. Identity and imported metadata appear inline in the vehicle card. The system photo picker grants access only to selected images. Before upload, the app applies orientation, limits images to 1600 pixels per axis, and re-encodes JPEG under 2 MiB without copying GPS or camera metadata. The server independently validates and strips metadata. A failed photo upload keeps the successfully saved vehicle and offers retry without creating another vehicle.

Network operations create W3C trace context for server correlation and emit bounded local structured logs containing method, status, and trace/span IDs. A separate authenticated request relays only an allowlisted operation name, duration, and HTTP status to the backend's client-events endpoint for Grafana traces and correlated logs. Delivery failures never block the user, and events are not queued or retried. Logs and events exclude addresses, tokens, vehicle details, form values, and coordinates. General OTLP credentials must never be placed in the app.

## Release boundaries

- Reminders are visible in the app; push and local notification scheduling are not implemented.
- Raspberry Pi and Smartcar integrations are available through the menu; receipt attachments and CrewChief AI remain checklist work.
- Costs use USD, odometers use miles, and fuel quantities use US gallons in this release.
- Signal charts display the canonical units supplied by the server, including km, km/h, kPa, Celsius, and percent. User-selectable conversions are not implemented.
- UI tests activate a transport stub only in Debug builds with `--ui-testing`. They use separate test Keychain/cache entries. Release builds contain no fixture transport.

## Working with imported history

Vehicle Overview shows only metrics with stored values, each with a small history preview. Numeric previews use lines and fill, states use categorical marks, and counts use bars. Requests are bounded to three simultaneous history fetches and reuse protected cached history offline. Tap a card to choose a reading type and history range, inspect values, and read the server's metric description and interpretation. Cards identify source, quality, and the authentic observation time or reporting period. Stale readings retain their value and gain a stale label. Daily or other period summaries remain explicitly historical.

History initially shows 30 days for recent readings, or one year when the selected reading type only has older values, so older imported snapshots are visible immediately. You can change the range using the picker. Calendar-only dates remain dates when choosing this default; they do not acquire a fabricated observation time.

Numeric readings use connected lines with a subtle fill. Counts and period totals use bars; boolean values and codes use categorical lanes with names supplied by the metric catalog, falling back to Off/On or numeric codes on older servers. Mixed buckets remain visibly Mixed because their exact transition times are unavailable. Grouped counts and totals show the bucket average and observed range, without inventing a cumulative total. Valid zero readings remain visible.

Charts preserve timestamped sample endpoints and bucket minimum/maximum ranges, breaking at empty buckets and long observation gaps. Date-only readings keep their calendar spacing, with breaks for missing days and no invented observation times or timezone. Each source and quality combination stays separate. Sparse date labels and a view fitted to the available data keep dense histories readable.

Tap the chart to inspect a bucket, or expand reading details for provenance and exact values. Loaded ranges remain readable offline, and a signal refresh failure does not mark unrelated garage functions offline. Collection details expose available diagnostic codes, missing-data coverage and recording segments without interpreting unknown diagnostic results as successful reads.

History searches titles, notes, tags, and custom fields. Filter by record type and open an entry to read its full notes, custom fields, and source reference. Undated notes remain undated and do not display invented mileage or spending. Odometer entries distinguish initial and final readings and retain whether the reading was measured, estimated, or unspecified.

Planned work appears in Upcoming, separately from completed history. Its cost remains an estimate even when its status is marked done. Log a service record separately to record actual work and spending. Editing a record sends only changed fields; imported provenance, links, tags, and untouched precision stay on the server.

Recurring reminders ask for the completion reading or date. Fixed schedules advance one interval from the existing due value, so a missed occurrence may leave the next one overdue. Flexible schedules advance from the completion values. Requests include the expected due values to prevent a retry from completing two occurrences. If the schedule changes elsewhere, review the refreshed reminder before completing it again.

Loaded migration metadata is available in the protected offline cache. Unknown future record kinds remain readable without breaking the whole vehicle history; editing those kinds requires an app update. The app does not import or restore LubeLogger database files itself.
