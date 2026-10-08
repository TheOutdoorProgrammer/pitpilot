# 3. Store vehicle signals with observation provenance and preserve summary conversions atomically

Date: 2026-10-08

## Status

Accepted.

## Context and Problem Statement

Vehicle measurements currently arrive as generated LubeLogger notes, preventing native dashboards and historical charts.
Daily summaries contain aggregates and diagnostics as well as actual timestamped readings.
Vehicle measurements are private business data and must remain separate from application health telemetry.

## Considered Options

1. Extend SQLite with canonical vehicle signals, structured contexts and transactional summary conversion.
2. Flatten every summary value into timestamped samples and delete the notes.
3. Use Prometheus as the primary vehicle history database.

## Decision Outcome

Chosen: **option 1**.

Store canonical numeric observations with explicit source, quality, actual observation times or aggregate intervals. Preserve diagnostic and recording context separately. Ingest immutable samples idempotently and allow only monotonic upstream revisions of aggregate identities. Convert recognized generated notes in one transaction that stores signals and original evidence before removing their visible records. Bind application to a state-specific preview and retain import ledger identities. Expose an opt-in OpenMetrics endpoint using a separate read-only credential; it reports current gauges and their observation age without adding private values to operational OTel.

## Consequences

### Good

- Native dashboards and bounded historical graphs can share one durable, backed-up source of truth.
- Strict parsing and archival evidence preserve source detail and prevent partial deletion.
- A dedicated scrape credential limits external collectors to read-only metrics.

### Bad

- SQLite stores growing observation history, so indexes and bounded history queries are required.
- Historical daily aggregates cannot recover the raw samples that produced them.
- The source evidence archive deliberately retains private text inside protected backups even after visible notes are removed.
- Canonical metric definitions and source adapters require explicit maintenance as upstream fields change.

### Rejected because

- Flattening daily aggregates invents observation times, loses ranges and diagnostic context, and makes graphs misleading.
- Prometheus scrape history would miss offline uploads and cannot atomically reconcile source-note conversion with garage records.
