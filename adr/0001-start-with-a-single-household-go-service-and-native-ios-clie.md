# 1. Start with a single household Go service and native iOS client

Date: 2026-10-08

## Status

Accepted.

## Context and Problem Statement

PitPilot needs a working Flux deployment and an installable native iOS app before the broader replacement checklist can be verified.
The initial workload is a single household; vehicle records and trip points must survive restarts without running a second database service.

## Considered Options

1. One Go service with SQLite on a persistent volume and a native SwiftUI client
2. PostgreSQL and PostGIS from the first release
3. Keep LubeLogger as the permanent backend

## Decision Outcome

Build a single Go service with SQLite, a versioned authenticated JSON API, and a native SwiftUI client. Deploy one replica with Recreate and a persistent volume. Use a revocable high-entropy household token stored in the iOS Keychain and server secret; multi-user authorization remains a separate checklist item. Store real recorded trip points without claiming route inference or spatial analytics. Preserve current vehicle pipelines while new integrations are developed.

## Consequences

### Good

- The first release has durable transactions without operating another server.
- Pure Go SQLite permits GoReleaser cross-compilation outside the container.
- The native client and API can be tested together before migration.

### Bad

- A single database writer and one application replica limit horizontal scaling.
- Household tokens do not provide per-person roles; rotating the token reconnects clients.
- Later spatial queries or larger deployments may require a PostgreSQL migration.

### Rejected because

- PostgreSQL/PostGIS adds an independently operated database before spatial query requirements exist; reconsider when route analysis or concurrency warrants it.
- A permanent LubeLogger backend retains API limitations and cannot satisfy the requested Go-owned backend, although existing integrations remain useful migration references.
