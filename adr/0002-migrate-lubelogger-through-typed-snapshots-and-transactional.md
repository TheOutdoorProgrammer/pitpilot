# 2. Migrate LubeLogger through typed snapshots and transactional reconciliation

Date: 2026-10-08

## Status

Accepted.

## Context and Problem Statement

LubeLogger APIs and CSV exports omit recurrence, custom-field types, source identities and some relationships. The installed source uses LiteDB. PitPilot uses SQLite and a native iOS client. Migration must preserve undated notes and estimated readings, stay repeatable while source integrations continue, and protect user edits.

## Considered Options

1. Read a consistent LiteDB snapshot with the official pinned reader, then reconcile typed JSON in Go
2. Import API or CSV projections directly
3. Implement a LiteDB page reader in Go

## Decision Outcome

Use an optional isolated LiteDB extraction helper and keep the PitPilot service and importer in Go. Preserve original domain documents alongside deterministic imported identities. Require preview before an atomic apply, reject unsupported data, and detect conflicting target edits. Keep source integrations running until a separate validated cutover.

## Consequences

### Good

- The official reader handles transaction-log recovery and BSON types.
- Source documents remain available for reconciliation and future support.
- Repeated imports do not duplicate records or silently replace edited targets.
- The native app can distinguish notes, readings, plans and completed work.

### Bad

- Extraction requires an optional .NET tool and a consistent protected source snapshot.
- The first importer supports miles, US gallons and USD; attachments and other nonempty unsupported collections block migration.
- Stock images stay in the original backup and are not recreated as PitPilot vehicle photos.

### Rejected because

- API and CSV alone cannot provide a faithful source archive.
- A new Go implementation of LiteDB storage and recovery adds unnecessary corruption risk.
