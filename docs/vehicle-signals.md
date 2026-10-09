# Vehicle measurements

Vehicle signals provide dashboard values and history without turning measurements into maintenance notes. Pi and Smartcar adapters submit the same authenticated ingestion format. See [device enrollment](device-api.md) and [Smartcar authorization](smartcar.md) for their authentication and setup flows.

## Ingestion

`POST /api/v1/vehicles/{id}/signals` accepts a batch with `source` (`pi`, `smartcar`, or `lubelogger`), a stable `batchId`, `observations`, and optional structured `contexts`. Use the household API bearer token over HTTPS. That credential currently grants household access; this endpoint does not provide device-specific permissions.

For example, an adapter can submit a measured intake manifold pressure:

```json
{
  "source": "pi",
  "batchId": "example-boot-42",
  "observations": [{
    "key": "example-boot-42-manifold",
    "metric": "manifold_kpa",
    "unit": "kPa",
    "statistic": "sample",
    "quality": "measured",
    "value": 38,
    "observedAt": "2026-06-01T12:00:00Z"
  }]
}
```

Use the source's actual observation time. Upload time is not a replacement for an unknown device clock. Values use the catalog's canonical units; do unit conversion before ingestion. Missing values are omitted, never replaced by zero.

Samples use `observedAt`. Aggregates use `periodStart` and `periodEnd`, with `min`, `max`, `mean`, `sum`, or `count` statistics. A date-only snapshot uses `statistic: "snapshot"`, `calendarDate`, and `timezone: "unknown"`, with no invented timestamps. `quality` distinguishes `measured`, `estimated`, and `derived` values.

Observation identity is scoped to the vehicle, source, and stable key. Repeating the same data is safe. A changed immutable sample conflicts. Aggregate updates require a genuinely newer upstream `sourceRevision` while retaining the same metric, statistic, and reporting period. Each revised delivery needs a new `batchId`; retries reuse that ID and the exact payload. Receipt time must not be used as an upstream revision.

Structured contexts preserve diagnostic observations, recording coverage, recording segments, and locations. A historical union of trouble codes means those codes appeared during the period; it does not prove that every code is still active. A recorded location is not a reconstructed route.

## Dashboard and history

`GET /api/v1/signals/catalog` lists supported metrics and units. `GET /api/v1/vehicles/{id}/signals/latest` supplies available measurements and their provenance. `GET /api/v1/vehicles/{id}/signals/history` selects a metric, statistic, time range, and bounded `maxPoints`. The native dashboard links each metric to its history.

History defaults to `statistic=sample`, seven days and 120 points per source, quality and statistic series. Use `statistic=all` to fetch every statistic in one request. Each series retains its source, quality, statistic and unit; count statistics use `count` even when the metric itself has another unit. Explicit `from` and `to` use RFC3339 timestamps; the maximum range is 366 days and `maxPoints` accepts 1 through 500. Calendar-date snapshots keep one point per date instead of timestamp buckets, so they can exceed `maxPoints` within the bounded date range.

Timestamped buckets retain `minimumObservedAt` and `maximumObservedAt` alongside first and last observation times. `maxGapSeconds` bounds the largest gap between retained source samples within a bucket, including zero for a single observation. Millisecond index ties can conservatively overstate that gap by less than one millisecond. These fields are absent for untimed summaries and date-only snapshots. Clients must not infer continuous coverage from the padded bucket width, count, or minimum and maximum alone. Period-summary bucket means are unweighted averages of the supplied summaries, not duration-weighted driving averages.

The native dashboard has a Customize dashboard metrics control. Show/hide choices persist per vehicle on that phone, including offline use, within its account-scoped protected cache. They do not stop collection or delete history. New metrics remain visible by default. Vehicle cards show vehicle information without import bookkeeping; imported records retain their source details.

Upgrading offline can reuse older separately cached statistics in the unified chart. The app chooses at most one compatible saved range per statistic, preserves original units and provenance, and excludes out-of-range or partially overlapping timed aggregates instead of trimming their values. This read-only fallback never overwrites saved entries; a fetched unified response takes precedence.

Latest sample time and staleness are distinct from the time PitPilot fetched the response. History presents one chart across LubeLogger, Pi and Smartcar sources. The default Observed readings view combines timestamped samples and date-only snapshots while retaining their precision. Period averages, extrema, totals and counts remain explicit choices within the same chart. Period averages use separate diamond markers without connecting lines or fill, so a daily average does not masquerade as the day's last observed speed. The default range widens to one year when any available source has older readings; 24-hour, 7-day and 30-day views remain available. Dashboard previews use the same observation semantics.

The native app selects a chart from the measurement's unit and statistic:

- Numeric readings use connected lines with a subtle fill and preserve observed ranges.
- Boolean values use Off/On lanes; codes use categorical lanes. Mixed buckets do not imply an exact transition time or a fractional state.
- Counts and period totals use bars. Grouped summaries show the bucket average and range, not an invented cumulative total.

Calendar readings keep their true day spacing as square markers centered within their reported day. This is a drawing coordinate only; no timestamp or timezone is added to the source. Timestamped readings retain exact intraday positions, with UTC date labels when shown alongside unknown-time calendar data. Rapid measurements such as speed, RPM and pressure connect only across sampling gaps of at most 30 seconds; fuel level, battery charge and odometer allow 15 minutes. Reduced dense history preserves first, last and extreme observations at actual timestamps. Unknown or discontinuous buckets retain their values and range whiskers without implying continuous shaded coverage. Only daily fuel, battery and odometer snapshots receive dotted, unshaded guides across gaps of up to seven days. Guides never join different sources. Source, quality and statistic remain visible, and estimated values use cyan. The chart fits available readings, uses sparse date labels, and keeps zero values visible. Tap to inspect original dates, source, quality, statistic and bucket values, or expand all reading details. Boolean and categorical charts never receive interpolated guides or fractional states.

## Private OpenMetrics scraping

Set `PITPILOT_METRICS_TOKEN_FILE` to a file containing a separate random token of at least 32 characters. This opts into `GET /metrics`. Authenticate with `Authorization: Bearer <scrape-token>`. The scrape token grants no garage API access, and the household token cannot substitute for it.

For a Prometheus-compatible collector:

```yaml
scrape_configs:
  - job_name: pitpilot-vehicle-signals
    scheme: https
    metrics_path: /metrics
    authorization:
      type: Bearer
      credentials_file: /run/secrets/pitpilot-metrics-token
    static_configs:
      - targets: [pitpilot.example.com]
```

Point Grafana at the collector's metrics datasource. The endpoint follows [OpenMetrics 1.0](https://prometheus.io/docs/specs/om/open_metrics_spec/). Observation timestamps are separate gauge values, so a fresh scrape does not imply a fresh vehicle reading. Scraping collects current values going forward; use the history API for previously stored observations.

This endpoint contains private vehicle measurements. It is separate from PitPilot's operational OpenTelemetry logs and traces and does not automatically send vehicle data to an external monitoring service. Protect the scrape credential and the collector's stored data as household data. Never embed either API credential in an app binary.

## Metric explanations

The signal catalog supplies a plain-language `description` and `interpretation` for every supported metric. Optional `valueLabels` map discrete state values to their actual meaning, such as readiness completion, ignition-monitor family and fuel-control mode. Native metric details display these alongside the existing source, quality and timing information. Explanations distinguish measurements from commands, state codes from quantities, and absolute pressure from gauge or manifold-relative pressure. They do not apply a universal healthy range across different engines.

The OBD channel identities and pressure references follow the existing collector decoders and [DashLogic's PID reference](https://www.dashlogic.com/docs/technical/obdii_pids). Fuel-trim wording was checked against [Snap-on's fuel trim explanation](https://www.snapon.com/EN/UK/Diagnostics/News-Centre/Technical-Focus-Archive/fuel-trim-adaptation); readiness completion is distinct from diagnosis in [Snap-on's vehicle communication guide](https://www.snapon.com/Files/Diagnostics/UserManuals/AsianImportVehicleCommunicationSoftwareManual_EAZ0025B02H.pdf). Oil life means estimated service life, as described by [Smartcar](https://smartcar.com/blog/introducing-the-engine-oil-life-api-production), rather than oil level.

## Convert generated notes

Back up the database first. The converter recognizes complete generated OBD driving summaries and Smartcar status notes imported from LubeLogger. Notes with unrecognized text or metadata remain visible. Conversion retains the full original record as protected evidence and stores structured observations and contexts in the same transaction that removes the visible note.

```sh
pitpilot convert-summary-metrics \
  --server https://pitpilot.example.com \
  --token-file /run/secrets/pitpilot-api-token
```

Review the `converted` and `preserved` counts, then repeat with `--apply` and the returned `previewToken`. Use `--database /private/rehearsal.db` instead of `--server` to rehearse on a backup copy. The API also provides `/api/v1/migrations/summary-metrics/preview` and `/apply` with a JSON `previewToken` field.

A stale preview, changed note, invalid signal, or failed write aborts the transaction. JSON export and database backup include original conversion evidence. Reimporting an unchanged converted LubeLogger note skips it. If its source later changes, import reports `converted-summary-source-changed` by default. The migration command's explicit `--refresh-converted-summaries` option previews replacement of supported generated summaries, preserves the previous source and measurements in a versioned archive, and updates only untouched conversion-owned rows. Customized notes, changed measurements, incomplete summaries and stale previews remain conflicts. See the [migration workflow](lubelogger-migration.md).

Conversion changes PitPilot only. It does not delete source LubeLogger records or redirect existing collectors.
