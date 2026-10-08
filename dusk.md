---
dusk: v1alpha1
namespace: stout
kind: repository
name: pitpilot
title: PitPilot
attributes:
  language: go, swift
  visibility: public
  source: https://github.com/TheOutdoorProgrammer/pitpilot
---

PitPilot is a vehicle maintenance and trip-history application with a Go backend and native SwiftUI client. CrewChief is its planned optional paid AI offering.

The initial backend stores household records in SQLite behind bearer-token authentication. Deploy one replica with persistent storage. The iOS app keeps credentials in Keychain and caches records for offline reading. Pi collection, automatic collector updates, Smartcar ingestion, migration, and paid AI remain checklist work rather than shipped capabilities.

Quill builds backend binaries with GoReleaser before assembling the runtime container. The Dockerfile only copies the prebuilt binary. The same release workflow signs the native app and publishes it through Fledge. Deployment endpoints, credentials, and infrastructure inventory belong in the private operations catalog.

Before replacing an existing vehicle pipeline, reconcile imported records, exercise the new integration, and verify backup restoration. Do not infer confirmed purchases or completed maintenance from telemetry alone. Operational logs and traces must exclude vehicle identifiers, locations, credentials, and record content.
