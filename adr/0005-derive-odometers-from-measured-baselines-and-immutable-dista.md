# 5. Derive odometers from measured baselines and immutable distance intervals

Date: 2026-10-09

## Status

Accepted.

## Context and Problem Statement

A saved measured odometer record did not update the dashboard because the dashboard used an unrelated vehicle field.
The native collector sends raw sensor observations while the retired application receiver estimates mileage independently.
The user chose collector-reported distance increments with backend-owned odometer calibration.

## Considered Options

1. Backend projection from measured baselines and immutable collector distance intervals
2. Collector-maintained absolute estimated odometer
3. Add every received distance to a mutable vehicle counter

## Decision Outcome

Chosen: **option 1**.

Store calibration baselines separately from original records and import provenance. New records may include their genuine capture time; date-only readings retain date-only precision and start contributing subsequent distance at server acceptance. Existing date-only readings use the migration acceptance boundary, which may omit intervening travel. The collector integrates only adjacent successfully queued speed samples in one process with consistent clocks and gaps no longer than 30 seconds. It emits immutable driving_distance_km sum observations with period bounds through the existing device-scoped idempotent queue and signal ledger. The backend sums only intervals wholly after the selected baseline, preserving fractional distance. A newer timestamped Smartcar absolute odometer replaces the baseline instead of being added. Overlapping interval groups are excluded with an explicit dashboard warning. Original absolute estimates never become deltas. Real capture times sort by their UTC instants. Date-only records retain their supplied calendar day, with acceptance clamped inside that day for ordering only; sorting bounds never become displayed measurement timestamps. SQLite schema 5 persists calibration state; physical backup and portable export preserve it.

## Consequences

### Good

- Manual corrections immediately recalibrate the dashboard without reflashing the collector.
- Retries and out-of-order delivery cannot double-count stable interval identities.
- Captured distance, unknown coverage and measured mileage remain distinguishable.
- Original imported records and hashes remain unchanged.

### Bad

- Date-only corrections conservatively discard earlier captured distance because their measurement instant is unknown.
- A later overlapping interval can retract an earlier estimate; the dashboard warns and never falls below its measured baseline.
- Raw interval history grows with collection and projection reads its covered intervals.
- Updating from schema 4 requires a backup; old binaries cannot open schema 5.

### Rejected because

- Collector-owned absolute odometers cannot see manual backend corrections while offline and introduce competing authoritative counters.
- Incrementing a mutable vehicle counter on receipt double-counts retries and incorrectly adds late intervals preceding a correction.
