---
dusk: v1alpha1
namespace: stout
kind: repository
name: pitpilot
title: PitPilot
attributes:
  language: go, swift
  source: https://github.com/TheOutdoorProgrammer/pitpilot
  visibility: public
---

PitPilot is a Go vehicle journal with a native Swift iOS app, a Raspberry Pi OBD/GPS collector, and Smartcar integration. It provides maintenance and cost records, typed vehicle metrics, measured odometer projection, vehicle photos, metric-aware charts, and location/trip history.

The Go API stores the journal, typed observations, source provenance and integration state in SQLite. Device-scoped credentials restrict collector access to its vehicle. The collector runs natively on Linux, keeps immutable deliveries in a private durable queue and removes them only after exact acknowledgement. Its signed automatic updater is independent of the unprivileged reader. GPS recording is opt-in and capability-negotiated; captured fixes support last-known location and derived trips without changing measured odometer history. Smartcar supports signed webhooks and polling with persisted provider backoff.

LubeLogger migration preserves source documents, relationships and current/prior converted-summary evidence. Administrative legacy cleanup verifies row ownership, preserves reused native readings and unrelated journal data, and records durable replay protections. Opaque history revisions invalidate retired cached telemetry while retaining offline records, photos and per-vehicle preferences. API contracts, schema migrations and backup behavior are documented in [docs/api.md](docs/api.md); collector installation and recovery are in [docs/picollector.md](docs/picollector.md).

Quill runs GoReleaser before the container copies the resulting binaries. Native tests and signed iOS archives use the self-hosted runner with same-run IPA provenance. The release workflow includes iOS by default; compatible backend/collector-only releases can explicitly omit it while retaining Go tests, security checks and collector signing. API and collector operations provide structured logs and distributed traces without recording private readings, identifiers or credentials.

Current deployed versions, image digests, Flux revisions, Fledge artifacts, verified backups and physical acceptance belong to `service:stout/pitpilot` and its attached Dusk runbooks. This repository declaration describes architecture and workflow, not deployment status. Check that service before releases or infrastructure work; historical verification does not establish acceptance of a newer source revision or physical device behavior.
