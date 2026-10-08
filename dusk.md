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

The backend stores household records in SQLite behind bearer-token authentication. Deploy one replica with persistent storage. The iOS app keeps credentials in Keychain and caches records for offline reading. Version 0.2.0 adds typed LubeLogger migration with previews, atomic application, duplicate prevention and conflict reporting. The supported projection preserves notes, custom fields, estimated readings, plans and reminder recurrence. Unsupported source data blocks import; the migration guide defines those limits. An online backup command captures committed WAL data without migrating the source database.

Install the matching native app before importing new record categories. Pi collection, automatic collector updates, Smartcar ingestion, attachments, offline writes and paid AI remain checklist work rather than shipped capabilities.

Quill builds backend binaries with GoReleaser before assembling the runtime container. The Dockerfile only copies the prebuilt binary. The same release workflow signs the native app and publishes it through Fledge. Deployment endpoints, credentials, and infrastructure inventory belong in the private operations catalog.

Before replacing an existing vehicle pipeline, reconcile imported records, exercise the new integration, and verify backup restoration. Do not infer confirmed purchases or completed maintenance from telemetry alone. Operational logs and traces must exclude vehicle identifiers, locations, credentials, and record content.
