---
dusk: v1alpha1
namespace: stout
kind: repository
name: pitpilot
title: PitPilot
attributes:
  image: ghcr.io/theoutdoorprogrammer/pitpilot:0.5.2@sha256:16fe84e886314208b62ce3e89d00523b4d951cb30dd9d15c560c5de9a9804018
  language: go, swift
  release_revision: 8e41cb3ec3ef2a82e40a96dbd2f2ffca6131ce4f
  schema: "7"
  source: https://github.com/TheOutdoorProgrammer/pitpilot
  validation: 76 native tests; backend race/vet/security; signed artifacts; exact receiver recovery, live history and restored backup verified.
  version: 0.5.2
  visibility: public
---

PitPilot is a Go vehicle journal with a native Swift iOS app, a Raspberry Pi OBD/GPS collector, and Smartcar integration. It provides maintenance and cost records, typed vehicle metrics, measured odometer projection, vehicle photos, metric-aware charts, and location/trip history.

Current release v0.5.2 uses SQLite schema 7. Dashboard metric visibility is saved per vehicle on each phone. Compatible imported and native observations share a chart, with explicit period-summary choices, original peak times and gaps based on actual recording coverage. A 24-hour range exposes recent detail; older offline caches remain readable. Receiver recovery archives original events, reuses exact native overlaps and leaves unknown-clock events unplotted. LubeLogger migration preserves typed source documents, relationships, native edits and current/prior converted summaries. GPS recording is opt-in and capability-negotiated. Smartcar scheduling persists a minimum hourly interval and provider backoff.

Quill runs GoReleaser before Docker copies the resulting binaries. Native tests and signed iOS archives use the self-hosted runner, with same-run IPA provenance. Release v0.5.2 passed 63 unit tests and 13 UI journeys, backend race/vet checks and reachable vulnerability checks. Screenshot review covers observed speed, explicit summaries, real gaps and peaks, fuel, states, counts and offline history. Live recovery reconciled 34,808 original timestamped readings across 31 metrics, preserving every existing row and vehicle/odometer response. The restored post-import backup retained all 19 tables exactly after repeat application. Grafana verified import and live history traces with correlated logs. Physical receiver/GPS, phone and provider acceptance remain separate operational checks.

Release: https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.5.2
Build: https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37919267547
Source: 8e41cb3ec3ef2a82e40a96dbd2f2ffca6131ce4f
