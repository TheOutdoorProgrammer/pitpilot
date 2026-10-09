# 7. Keep bounded vehicle photos inside the garage database

Date: 2026-10-09

## Status

Accepted.

## Context and Problem Statement

Vehicles need user-selected photos without exposing a public file server or leaking embedded location metadata.
PitPilot already relies on a single SQLite database and consistent online backups for self-hosted recovery.

## Considered Options

1. Store bounded sanitized JPEG blobs in SQLite alongside the vehicle metadata.
2. Store files on the application volume and reference them from SQLite.
3. Add external object storage and signed photo URLs.

## Decision Outcome

Chosen: **option 1**.

Keep one JPEG of at most 2 MiB and 1600 pixels per side per vehicle in a cascading SQLite table. Authenticated endpoints decode and re-encode uploads before storing them, removing EXIF and appended payloads. A content hash identifies each revision. Metadata and bytes change in one transaction; portable exports include the bytes and database backups include everything.

## Consequences

### Good

- A backup cannot lose a referenced photo or require a second coordinated snapshot.
- Photo reads use existing household authentication and never need a public URL.
- Strict dimensions bound decoder memory; re-encoding removes embedded location metadata.

### Bad

- Images enlarge the database, portable exports and backups.
- JPEG normalization is lossy and excludes transparent or animated originals.
- The design is intentionally limited to small vehicle photos; general document attachments need their own limits and contracts.

### Rejected because

- Filesystem storage was rejected because it introduces non-transactional writes and separate backup consistency requirements for one small image per vehicle.
- Object storage was rejected because credentials, lifecycle management and another service are disproportionate for the current self-hosted garage.
