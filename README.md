# PitPilot

Vehicle maintenance, trip history, and connected vehicle data, with a Go backend and a native iOS app.

PitPilot is being planned as a replacement for LubeLogger that brings maintenance records and driving history into one application. Connect a supported vehicle through Smartcar or use a Raspberry Pi with an OBD adapter. Record data when connectivity is unavailable and sync it when the connection returns.

The Raspberry Pi setup should be manageable from the app: pair a device, assign a vehicle, check its connections, and see whether data has uploaded. Routine setup and troubleshooting should not require SSH.

## Status

This repository starts with a [feature checklist](FEATURES.md). There is no installable application yet. Features described here are planned.

## What we're building

- A garage with maintenance history, fuel and charging records, receipts, reminders, and ownership costs.
- A native iOS app with offline access, receipt capture, and trip maps.
- Built-in Smartcar support and a Raspberry Pi collector with durable offline storage.
- Trip recording and replay, followed by route comparisons and a map of roads traveled.
- Import from LubeLogger and exports that keep vehicle history portable.

Every reading should identify its source and when it was observed. Estimated mileage, inferred fuel-ups, and suggested maintenance must remain distinguishable from measured data and confirmed records.

## CrewChief AI

CrewChief is an optional paid AI offering planned for PitPilot. Proposed capabilities include answering questions about vehicle history, extracting draft records from receipts, explaining diagnostic observations, and suggesting maintenance to review.

Answers should cite the records or documentation they use and acknowledge missing evidence. Changes to maintenance history require confirmation. Location history is shared with an AI provider only when the user explicitly chooses to include it.

The proposed product split keeps ordinary recordkeeping, device ingestion, maps, and data export usable without an AI subscription. Pricing, usage limits, model providers, and distribution terms remain undecided. Third-party vehicle connectivity may have separate costs and requirements.

## Technical direction

The backend will use Go, and the iOS app will be native. A Raspberry Pi collector will upload authenticated observations to the backend. Existing collection and integration code will be evaluated for reuse before new implementations are written.

Database choice, hosting, authentication, and the app's minimum iOS version still need decisions. Self-hosting is a proposed requirement. Consequential architecture choices will be recorded in ADRs once evaluated.

## Location and vehicle support

Route history requires timestamped location observations. OBD speed alone cannot establish a route. GPS hardware or phone-based recording must be validated for the Pi workflow.

Smartcar signal availability and update frequency vary by vehicle and manufacturer. Compatibility and recording gaps must be visible in the app; detailed route coverage is not yet verified.

## Development and licensing

Work is tracked in [FEATURES.md](FEATURES.md). An item is complete when its acceptance checks pass, including relevant failure cases and verification in the environment where it runs.

Licensing and contribution terms will be selected before accepting code contributions. A public repository alone does not grant an open-source license. Initial documentation was drafted with AI assistance.
