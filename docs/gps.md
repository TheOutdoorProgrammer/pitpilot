# USB GPS and automatic trips

PitPilot can collect real USB GPS fixes independently of the OBD adapter. Enable **GPS recording** for the vehicle's paired Pi in the app. It is off by default. A missing receiver leaves OBD collection, delivery, health reporting and automatic updates running. Bluetooth OBD readings do not imply access to the phone's GPS.

The Pi refreshes recording policy every 15 seconds while connected and caches it privately for offline starts. A remote change takes effect when the Pi next contacts PitPilot. Disabling recording stops new fixes; existing queued fixes still upload. Revoke the device to stop all authorized uploads. Collection never changes Wi-Fi, Bluetooth binding, receiver configuration or the Pi's physical power policy.

## Receiver discovery

Automatic discovery recognizes exactly one USB device with u-blox vendor `1546` and product `01a6` or `01a7`. These are documented in [Stratux's USB rules](https://raw.githubusercontent.com/stratux/stratux/master/debian/10-stratux.rules). It probes 9600, 4800, 38400 and 115200 baud by reading checksummed NMEA output. It sends no receiver commands. Multiple matching receivers require an explicit selection.

[Stratux's GPS documentation](https://github.com/stratux/stratux/blob/master/docs/hardware/gps.md) identifies VK-162 revisions using u-blox 6 and 7. A model name alone does not establish the attached revision, serial path, baud, permissions or reception. Physical acceptance of the purchased receiver is still pending.

An optional `gps` object in `/etc/pitpilot/collector.json` overrides discovery. Inspect the actual device first, then supply its stable path and verified baud. For example, replace the placeholder below with the actual `/dev/serial/by-id/` link:

```json
{
  "gps": {
    "serialPort": "/dev/serial/by-id/REPLACE_WITH_VERIFIED_RECEIVER",
    "baud": 9600,
    "sampleSeconds": 5
  }
}
```

Use either `serialPort` or `deviceGlob`, never both. A glob must remain within `/dev/serial/by-id/` and resolve to exactly one character device. It cannot resolve to the configured OBD adapter. Sampling accepts 5 through 60 seconds. A local override selects hardware; the app's recording policy still controls whether it runs. If an older binary must be restored, remove a newly added local `gps` object first: old releases strictly validate their local configuration. Automatic discovery requires no configuration-file change and preserves that rollback path.

## Actual fixes and trip boundaries

Each accepted fix joins matching RMC and GGA UTC times, validates both checksums and coordinates, and retains the RMC date. RMC must report a valid fix and explicit GNSS mode. GGA must report satellite-based quality, at least three satellites and valid HDOP. Dead reckoning, simulation, missing dates, stale receiver dates, invalid coordinates and mismatched sentences are rejected. The host also requires a synchronized clock. Zero latitude or longitude is a valid measured coordinate, not a missing-value sentinel.

The parser follows the [u-blox 6 receiver protocol specification](https://content.u-blox.com/sites/default/files/products/documents/u-blox6_ReceiverDescrProtSpec_(GPS.G6-SW-10018)_Public.pdf). Speed is converted from knots to km/h. Course, altitude, satellites, fix quality and dimensionless HDOP remain optional or measured metadata. HDOP is never relabeled as accuracy in meters. Older NMEA output without an explicit RMC mode is intentionally insufficient to distinguish real fixes from dead reckoning.

The backend derives trips from those canonical fixes in capture-time order. Movement begins at a reported speed of 3 km/h; five stationary minutes close a trip. Routes under ten observed meters are omitted. Gaps longer than 120 seconds and implausible jumps split routes. Reconnecting the receiver, restarting the collector or crossing a UTC day creates a new recording segment; PitPilot does not join those segments into an invented continuous path. A recording segment is bounded to 20,000 fixes, above a full day at the default five-second sampling interval.

Trip distance is the approximate sum of accepted GPS segments. It never changes the odometer or duplicates OBD-derived distance increments. Late fixes can refine a provisional trip and its identity. Stationary fixes still update the last-known location without creating a trip. The app labels the real capture time; an old location remains last known, not a claim that the car is currently there.

## Delivery, history and privacy

GPS fixes use the existing device-scoped authenticated signals endpoint and immutable bbolt queue. A separate pending index keeps GPS deliveries out of the legacy OBD receiver feed and allows older rollback binaries to leave them intact. An old server that rejects GPS metadata leaves those deliveries queued until a compatible server acknowledges them. Capacity preserves old deliveries and reports rejected new samples. Interrupted writes, retries and out-of-order uploads do not manufacture extra routes or odometer distance.

`GET /api/v1/vehicles/{id}/location` returns `{"location":null}` or an actual fix with `source:"pi-gps"`, coordinates, `recordedAt` and available quality metadata. Trip lists default to 50 entries, accept a maximum of 100, and paginate with the last entry's `startedAt` as `before` and its `id` as `beforeId`. Listed routes contain at most 1,000 display points; `recordedPointCount` and `routeSimplified` disclose reduction. Gap endpoints are retained where capacity allows. Full fixes remain in the private export.

Deleting an automatic trip removes its canonical fixes and persists a recording/time exclusion. Clearing GPS history removes native GPS fixes and automatic trips while retaining a deletion cutoff. Offline replay cannot resurrect the deleted time range. Manual trips, other vehicle measurements and odometer history remain intact. A later fresh fix can establish a new last-known location when recording is enabled.

Coordinates, raw NMEA, serial paths and vehicle/device identifiers never enter operational telemetry. GPS state transitions and queue operations use bounded states and existing trace boundaries. Exports and database backups contain private location history and must be protected accordingly.

## Hardware acceptance

Software fixtures and cross-compilation do not prove a physical receiver works. Before declaring installation complete, verify the attached USB identity and stable path, collector-account serial access, synchronized host time, checksummed RMC/GGA output with an outdoor fix, actual authenticated delivery and a correctly timestamped map position. Then verify unplug/reconnect behavior, offline queued replay and physical power interruption without disturbing OBD or the retained receiver feed. Keep any diagnostic capture private and delete it when no longer needed.
