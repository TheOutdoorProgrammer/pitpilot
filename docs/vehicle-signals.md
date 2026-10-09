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

History defaults to seven days and 120 points per source and quality series. Explicit `from` and `to` use RFC3339 timestamps; the maximum range is 366 days and `maxPoints` accepts 1 through 500. Calendar-date snapshots keep one point per date instead of timestamp buckets, so they can exceed `maxPoints` within the bounded date range.

Latest sample time and staleness are distinct from the time PitPilot fetched the response. Historical daily summaries remain labeled as summaries. History keeps source and quality separate and preserves bucket minimum and maximum values. Calendar-date snapshots use a calendar axis, without claiming a time of day or timezone.

The native app selects a chart from the measurement's unit and statistic:

- Numeric readings use connected lines with a subtle fill and preserve observed ranges.
- Boolean values use Off/On lanes; codes use categorical lanes. Mixed buckets do not imply an exact transition time or a fractional state.
- Counts and period totals use bars. Grouped summaries show the bucket average and range, not an invented cumulative total.

Calendar readings keep their true day spacing, with line breaks for missing days. Timestamped sample lines break at empty buckets and observation gaps longer than 15 minutes. This drawing limit does not establish a source's sampling cadence; bucketed history cannot reconstruct every missing interval. The chart fits available readings within the selected range, uses sparse date labels, and keeps zero values visible. Tap a chart to inspect a bucket or expand reading details for provenance.

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

## Convert generated notes

Back up the database first. The converter recognizes complete generated OBD driving summaries and Smartcar status notes imported from LubeLogger. Notes with unrecognized text or metadata remain visible. Conversion retains the full original record as protected evidence and stores structured observations and contexts in the same transaction that removes the visible note.

```sh
pitpilot convert-summary-metrics \
  --server https://pitpilot.example.com \
  --token-file /run/secrets/pitpilot-api-token
```

Review the `converted` and `preserved` counts, then repeat with `--apply` and the returned `previewToken`. Use `--database /private/rehearsal.db` instead of `--server` to rehearse on a backup copy. The API also provides `/api/v1/migrations/summary-metrics/preview` and `/apply` with a JSON `previewToken` field.

A stale preview, changed note, invalid signal, or failed write aborts the transaction. JSON export and database backup include original conversion evidence. Reimporting an unchanged converted LubeLogger note skips it. If its source later changes, import reports `converted-summary-source-changed` instead of silently recreating the note or overwriting historical signals.

Conversion changes PitPilot only. It does not delete source LubeLogger records or redirect existing collectors.
