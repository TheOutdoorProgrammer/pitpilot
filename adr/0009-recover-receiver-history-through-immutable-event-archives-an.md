# 9. Recover receiver history through immutable event archives and transactional reconciliation

Date: 2026-10-09

## Status

Accepted.

## Context and Problem Statement

Legacy receiver backups retain original OBD events that LubeLogger daily summaries discarded from their visible notes.
Summary averages and last readings do not recreate a continuous driving history.
Recovery must preserve source evidence, avoid duplicating current collector readings, and retain unknown-clock events without inventing timestamps.

## Considered Options

1. Archive original events and import validated missing readings in a preview-bound transaction
2. Replay legacy uploads through the live collector ingestion endpoint
3. Replace imported summaries with reconstructed samples using a one-off database script

## Decision Outcome

Chosen: **option 1**.

Use a supported recovery command that reads a closed receiver backup without writing it. Validate canonical event identities and payloads, share the collector's observation mapping, and archive exact events with normalized measurements and overlap decisions in the garage database. Match existing Pi readings only by exact timestamp, metric, units, quality and value; conflicting identities or values abort the transaction. Preview binds the input and relevant target state. Applying it atomically archives every event and inserts only missing observations. Unknown-clock events remain archived without chart measurements. Repeated application does no work. Preserve original summary conversions and export the recovery evidence. Do not replay live side effects such as odometer increments, trips or receiver acknowledgements.

## Consequences

### Good

- Recovers actual timestamps and values instead of fabricating detail from daily summaries.
- Preserves original evidence and makes retries, overlap and conflicts auditable.
- Keeps live collection, odometer calibration and existing converted notes intact.

### Bad

- An additional archive table increases database and export size and requires a schema migration.
- Strict conflicts may require operator investigation before a recovery can proceed.
- Unknown clocks and missing recording coverage remain unresolved; the importer cannot manufacture complete trips.

### Rejected because

- Live upload replay couples historical recovery to collector authentication and side effects and does not preserve unknown-clock source evidence.
- One-off replacement SQL bypasses application invariants, discards proven summary provenance and is difficult to retry or audit safely.

