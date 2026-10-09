---
dusk: v1alpha1
namespace: stout
kind: repository
name: pitpilot
title: PitPilot
attributes:
  image: ghcr.io/theoutdoorprogrammer/pitpilot:0.5.0@sha256:dc92ac17abf0989010584ff038d6d8d25c797e011bf6331a1f1791e134b3dd62
  language: go, swift
  release_revision: ca1c6f3c1349b38ab41b3214209b801ace44c9af
  schema: "6"
  source: https://github.com/TheOutdoorProgrammer/pitpilot
  validation: 64 native tests; backend race/vet; reachable vulnerabilities clean; signed collector and same-run IPA provenance verified.
  version: 0.5.0
  visibility: public
---

PitPilot is a Go vehicle journal with a native Swift iOS app, a Raspberry Pi OBD/GPS collector, and Smartcar integration. It provides maintenance and cost records, typed vehicle metrics, measured odometer projection, vehicle photos, metric-aware charts, and location/trip history.

Current release v0.5.0 uses SQLite schema 6. LubeLogger migration preserves typed source documents, relationships, native edits and original converted summaries; explicit catch-up keeps prior source and metric versions recoverable. GPS recording is opt-in and capability-negotiated so older collectors can still update. Smartcar scheduling persists a minimum hourly interval and provider backoff across manual refresh, reconnect and restart.

Quill runs GoReleaser before Docker copies the resulting binaries. Native tests and signed iOS archives use the self-hosted runner, with same-run IPA provenance. Release v0.5.0 passed 64 native tests, backend race/vet checks and reachable vulnerability checks. Deployment acceptance includes schema migration preservation, restored backups, and live API/Grafana verification. Physical receiver/GPS and provider availability remain separate operational checks.

Release: https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.5.0
Build: https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37883761950
Source: ca1c6f3c1349b38ab41b3214209b801ace44c9af
