# 6. Derive automatic trips from scoped durable GPS fixes

Date: 2026-10-09

## Status

Accepted.

## Context and Problem Statement

PitPilot needs real vehicle locations and automatic trip history from a Pi USB receiver while the vehicle is frequently offline.
The existing device signal endpoint and bbolt delivery queue already provide scoped authorization, durable acknowledgements and replay identity.
Coordinates and trip distance must not become fabricated odometer readings, and deleting private routes must survive queued replay.

## Considered Options

1. Extend canonical location contexts, reuse durable delivery and derive bounded backend trip projections
2. Run gpsd plus a separate location transport and trip service
3. Let the Pi own finalized absolute trips and odometer totals

## Decision Outcome

Chosen: **option 1**.

Use a read-only NMEA RMC/GGA parser with mandatory checksums, matching UTC times, valid GNSS mode and explicit quality. Collection defaults off and follows a separately persisted device GPS policy. Negotiate config capabilities to keep old strict updaters working. Enumerate only recognized USB receivers or an explicit local serial override, probe baud by validating incoming sentences, and never write receiver or networking configuration.
Queue GPS independently from legacy OBD delivery within the existing durable database. Store immutable device-namespaced fixes through the signal endpoint, then deterministically project trips per collector connection and UTC day. Split missing intervals, implausible jumps and long stops. Keep GPS-derived route distance separate from OBD-based odometer increments. Materialize trips for bounded pagination, retain raw fixes in exports and preserve deletion cutoffs/exclusions so late replay cannot resurrect private routes.

## Consequences

### Good

- One authorization and durable transport model covers offline GPS and existing vehicle signals.
- Actual timestamps and quality remain available without treating HDOP as meters or a phone hotspot as a location source.
- Server recomputation handles duplicate and out-of-order uploads consistently, while deletion tombstones survive retries.
- Optional hardware and missing fixes do not block OBD, updater health or legacy receiver delivery.

### Bad

- Rebuilding an affected bounded recording costs more CPU than an arrival-order-only accumulator; each recording is limited to 20000 fixes.
- Trips split at GPS reconnects and UTC day boundaries. Earlier offline backfill can change a derived trip identity or merge provisional segments.
- GPS route distance is approximate and can undercount gaps; this deliberately does not recalibrate the odometer.
- A remote collection-policy change cannot reach an offline Pi until its next contact; it keeps the last explicitly persisted policy.
- Displayed routes are reduced to at most 1000 points, preserving discontinuity endpoints; full-fidelity export remains separate.
- The purchased receiver still requires physical acceptance of its USB identity, signal acquisition, placement and power-loss recovery.

### Rejected because

- A separate gpsd service and transport duplicate deployment, authorization and queue state without a current need for multiple local consumers.
- Pi-owned finalized trips cannot deterministically reconcile reordered server history and invite conflicting absolute odometer ownership.
