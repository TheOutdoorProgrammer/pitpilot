# Migrate from LubeLogger

Use a protected source snapshot, rehearse the import, then apply the same preview against your PitPilot server. LubeLogger stays usable throughout preparation. This workflow does not redirect existing collectors or remove the source installation.

## What is supported

The importer supports LiteDB exports containing vehicles, service, repair, upgrade, fuel, one-time expense, odometer, note, planned-work and reminder records. It preserves tags, typed custom fields, note pinning, partial-fill flags, plan status/priority and reminder relationships. Undated notes remain undated. Planned costs stay separate from actual spending. Recurring reminders retain their interval and anchoring behavior.

Original domain documents are retained separately from editable records and included in authenticated PitPilot JSON exports. Unknown source fields are archived, not claimed as implemented product features. Extra-field definitions are archived. Authentication collections are excluded. Treat both source and PitPilot backups as private household data.

The current projection supports **USD, miles and US gallons**. Other units, EV consumption, engine hours, adjusted odometers, recurring taxes, attachments, custom vehicle images, supply/equipment references and other nonempty unsupported collections block import. Do not relabel data to pass validation. Stock vehicle images remain in the original backup. Their source paths are archived but PitPilot does not render them as vehicle photos.

Odometer provenance remains unknown unless the source explicitly identifies it. The existing `Source` custom field value `Estimated from recorded speed` maps to an estimated reading, with the field and original document retained. Notes about inferred consumption never become confirmed fuel purchases.

## Capture and extract

1. Identify the database engine and installed LiteDB version. PostgreSQL requires a different extractor and is not supported by this workflow.
2. Capture a consistent database **and matching WAL**, configuration and assets with writers stopped, or use an atomic storage snapshot. Do not rely on a live file copy or the built-in backup omitting the WAL. Keep the protected original and hashes outside the checkout.
3. Verify the source resumes normally. Work only on a copy.
4. Build and test the [optional official LiteDB extractor](../tools/lubelogger-extract/README.md). It opens a private disposable clone, recovers its WAL, and emits typed JSON. Use the documented filesystem/network isolation for private extraction.
5. Verify units and source-server timezone from the installation. The stored UTC instant is not necessarily the source calendar date. Do not substitute the phone's timezone.

The extractor includes every collection in its private output. The Go CLI validates the projection locally and removes authentication collections before sending it to PitPilot. Keep exports out of Git, CI artifacts, telemetry and issue attachments.

## Rehearse locally

Build the Go CLI or use `go run ./cmd/pitpilot` in place of `pitpilot`. The command appears under `pitpilot --help` and `joey search pitpilot`.

```sh
umask 077
pitpilot migrate-lubelogger \
  --input /private/migration/export.json \
  --database /private/migration/rehearsal.db \
  --source household-lubelogger \
  --timezone UTC --currency USD --distance-unit mi --fuel-unit us-gal
```

Use values verified for your source. `--source` is a stable namespace, not an endpoint or credential. Reuse it for every snapshot from the same installation. Changing it creates a different import identity and can create another copy of your garage.

The first successful import pins its timezone, currency, units and export version. Later imports using that source name reject different interpretation settings, preventing a changed timezone from silently shifting historical dates.

Without `--apply`, the command previews only. It reports source counts, exact integer cost totals, created/updated/skipped/conflicting objects, records retained after source removal, and a `previewToken`. A local rehearsal creates the database/schema but imports no records during preview.

Repeat the command with `--apply TOKEN_FROM_PREVIEW` to commit the rehearsal. An outdated preview or any conflict aborts the entire transaction. Repeat a fresh preview after application: an unchanged snapshot should report every object skipped, with no creates or updates. Reconcile record counts, totals, notes, dates, custom fields, relationships and estimated readings before proceeding.

## Import into your server

Back up the target first. Install the matching native app before relying on new categories. Replace `--database` with an HTTPS server and a private token file:

```sh
pitpilot migrate-lubelogger \
  --input /private/migration/export.json \
  --server https://pitpilot.example.com \
  --token-file /private/pitpilot-api-token \
  --source household-lubelogger \
  --timezone UTC --currency USD --distance-unit mi --fuel-unit us-gal
```

Review that server's report, then repeat with its `--apply TOKEN_FROM_PREVIEW`. Preview tokens are bound to the batch and target state; a rehearsal token is not a substitute for reviewing the live target. The CLI rejects redirects and non-HTTPS origins. Tokens are read from files, never command-line values. Requests are limited to 4 MiB; larger migrations need batching support before proceeding.

Check the garage, full notes, custom fields, odometer provenance, upcoming plans and reminder thresholds in the app. Take a fresh target backup and verify restoration. Keep the source snapshot and extraction output as independent recovery evidence.

## Reruns and conflicts

- Same source and unchanged data: skip, including when you edited the PitPilot projection.
- Changed source and untouched PitPilot projection: update from the source atomically.
- Both changed: report a conflict and apply nothing. Reconcile the difference before trying again.
- Deleted PitPilot target: conflict; imports do not resurrect it silently.
- Source record removed: retain the PitPilot copy and report it. Imports never infer deletion from an incomplete source snapshot.

There is no forced-overwrite switch. Keep source writers in place until their replacements are validated. If they continue writing, capture a fresh consistent snapshot and reconcile again before final cutover.

This migration scope does not imply complete LubeLogger feature parity. See [the checklist](../FEATURES.md) for remaining attachments, units, reports, supplies, collaboration and integration work.
