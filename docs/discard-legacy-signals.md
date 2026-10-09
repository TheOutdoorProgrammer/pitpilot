# Discard imported telemetry

`pitpilot discard-legacy-signals` permanently removes current LubeLogger telemetry and measurements created by legacy receiver recovery from an existing PitPilot database. It requires direct database access and has no network deletion endpoint. Back up the database, retain the original source backup separately and rehearse on a copy first.

```sh
pitpilot discard-legacy-signals --database rehearsal.db
pitpilot discard-legacy-signals --database rehearsal.db --apply PREVIEW_TOKEN
```

Preview is the default. It returns counts and a token binding vehicle, telemetry, ownership, import and deletion-policy state. Apply requires that exact token and commits one transaction. New native readings or changed source evidence invalidate an earlier preview. After an interrupted command, preview again to establish whether it committed.

The command removes all observations and contexts whose source is `lubelogger`. For receiver recovery, it validates each raw event, original identity, exact projected values, timestamps, units, database row metadata and deterministic recovery batch marker before deleting owned Pi rows. Native Pi readings reused during recovery retain their original keys and values. Arbitrary rows with a `receiver:` prefix are not sufficient evidence of ownership and are not automatically deleted. Corrupted or missing ownership evidence aborts the entire transaction.

Receiver event archives are removed after their projections are verified. Existing native Pi and Smartcar readings, maintenance records, reminders, odometer baselines, trips, vehicles, devices, photos, source-import mappings, converted-note archives and deduplication markers remain intact. Converted-note archives remain private migration evidence; they are not current telemetry or visible notes. Repeating cleanup creates no measurements or further changes.

Schema 8 adds a vehicle-scoped `legacySignalPolicies` collection to JSON exports. The policy blocks later LubeLogger signal ingestion, converted-summary refresh and receiver recovery for affected vehicles, returning a conflict instead of silently recreating discarded data. Native ingestion and vehicles without such a policy remain available. There is no policy-reset option; restoring discarded history requires a deliberate recovery procedure using the secured backup.

The latest-signals response has an optional opaque `historyRevision` string. It appears when the policy is created and stays stable on repeat cleanup. Clients invalidate cached history for that vehicle when the revision changes, including when an updated app connected before server-side cleanup completed.

Opening an older database upgrades it to schema 8. Earlier server versions refuse that schema. Rollback requires restoring a verified pre-upgrade backup with the server stopped, not editing the schema version. Operational logs and traces contain bounded operation names and counts, never source values or raw identifiers.
