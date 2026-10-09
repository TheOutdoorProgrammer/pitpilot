---
dusk: v1alpha1
namespace: stout
kind: repository
name: pitpilot
title: PitPilot
attributes:
  image: ghcr.io/theoutdoorprogrammer/pitpilot:0.5.1@sha256:a84b40292f0d62a4bdfc675fb2265e6a709c89c6e4552d3f1c687f209045dfb7
  language: go, swift
  release_revision: 85c2e625a969b3b73205fac317f1b30bdc1da8d1
  schema: "6"
  source: https://github.com/TheOutdoorProgrammer/pitpilot
  validation: 71 native tests; backend race/vet; reachable vulnerabilities clean; signed collector and same-run IPA provenance verified.
  version: 0.5.1
  visibility: public
---

PitPilot is a Go vehicle journal with a native Swift iOS app, a Raspberry Pi OBD/GPS collector, and Smartcar integration. It provides maintenance and cost records, typed vehicle metrics, measured odometer projection, vehicle photos, metric-aware charts, and location/trip history.

Current release v0.5.1 uses SQLite schema 6. Dashboard metric visibility is saved per vehicle on each phone, and compatible imported and native readings share a chart with explicit sparse-gap guides. Unified history preserves source, statistic, units, quality and time precision; offline upgrades can reuse legacy history caches. LubeLogger migration preserves typed source documents, relationships, native edits and original converted summaries; explicit catch-up keeps prior source and metric versions recoverable. GPS recording is opt-in and capability-negotiated so older collectors can still update. Smartcar scheduling persists a minimum hourly interval and provider backoff across manual refresh, reconnect and restart.

Quill runs GoReleaser before Docker copies the resulting binaries. Native tests and signed iOS archives use the self-hosted runner, with same-run IPA provenance. Release v0.5.1 passed 59 unit tests and 12 UI journeys, backend race/vet checks and reachable vulnerability checks. Screenshot review covers unified history, metric controls, offline restoration and typed charts. Deployment acceptance includes data preservation, restored backups, and live API/Grafana verification. Physical receiver/GPS and provider availability remain separate operational checks.

Release: https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.5.1
Build: https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37911967530
Source: 85c2e625a969b3b73205fac317f1b30bdc1da8d1
