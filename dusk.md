---
dusk: v1alpha1
namespace: stout
kind: repository
name: pitpilot
title: PitPilot
attributes:
  image: ghcr.io/theoutdoorprogrammer/pitpilot:0.5.3@sha256:325e6e5d2f9109833fe63e9a3ff3fd89a1cfdaa071c380f978f269290dd50b5e
  language: go, swift
  release_revision: 50617ee9aa27a9b482d55083b5e21a80b1bc7a05
  schema: "8"
  source: https://github.com/TheOutdoorProgrammer/pitpilot
  validation: 76 native tests; backend race/vet/security; signed artifacts; scoped legacy deletion, retained native history and restored backup verified.
  version: 0.5.3
  visibility: public
---

PitPilot is a Go vehicle journal with a native Swift iOS app, a Raspberry Pi OBD/GPS collector, and Smartcar integration. It provides maintenance and cost records, typed vehicle metrics, measured odometer projection, vehicle photos, metric-aware charts, and location/trip history.

Current release v0.5.3 uses SQLite schema 8 and restores the v0.5.1 chart presentation after owner feedback. The supported legacy-discard command verifies row ownership before removing imported observations and contexts, preserves reused native readings and journal data, and records a durable replay policy. App cache migration and server history revisions remove retired cached telemetry while preserving offline records, photos and per-vehicle dashboard preferences. LubeLogger migration retains source documents, relationships and current/prior converted-summary evidence. GPS recording is opt-in and capability-negotiated. Smartcar scheduling persists a minimum hourly interval and provider backoff.

Quill runs GoReleaser before Docker copies the resulting binaries. Native tests and signed iOS archives use the self-hosted runner, with same-run IPA provenance. Release v0.5.3 passed 64 unit tests and 12 UI journeys, backend race/vet checks and reachable vulnerability checks. Reviewed screenshots confirm restored numeric lines/fill, state lanes, count bars, dashboard visibility and offline history. Live cleanup removed 34,911 legacy observations and 8,215 contexts while preserving all 1,514 native observations, 159 native contexts and unrelated records. All 31 native metric histories and unchanged vehicle/odometer views passed live checks. A restored backup retained all 20 tables exactly after repeat cleanup. Grafana verified discard and history traces with correlated logs and no observed runtime/exporter errors. Physical receiver/GPS, phone and provider acceptance remain separate operational checks.

Release: https://github.com/TheOutdoorProgrammer/pitpilot/releases/tag/v0.5.3
Build: https://github.com/TheOutdoorProgrammer/pitpilot/actions/runs/37939841137
Source: 50617ee9aa27a9b482d55083b5e21a80b1bc7a05
