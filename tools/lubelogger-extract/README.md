# LubeLogger snapshot extractor

Export a consistent, offline LubeLogger LiteDB snapshot into lossless extended JSON for PitPilot migration.

This optional migration helper uses the official LiteDB 5.0.17 reader, matching the source database version. PitPilot itself remains Go. Opening a disposable clone with the source library preserves WAL recovery and BSON types without implementing another database reader.

## Prepare the source

Obtain a consistent snapshot of the data directory while every writer is stopped, or use an atomic storage snapshot. Keep the original database **and its matching transaction log**, configuration, uploaded files, and hashes in a protected backup. A live file copy or LubeLogger's built-in backup alone is not a consistency guarantee. Do not point this tool at a running installation.

For `cartracker.db`, the adjacent transaction log is `cartracker-log.db`. The helper hashes both source files before and after copying, compares the clone hashes, and opens only its private disposable clone. Source-hash stability detects changes during copying; it cannot establish that an already inconsistent snapshot was good. LiteDB recovery and the `UTC_DATE` pragma can modify the clone, never the supplied snapshot.

## Build and verify

Requires .NET 9 SDK on macOS or Linux. Restore dependencies and build before allowing access to private data. The package version is exact and transitive package hashes are recorded in `packages.lock.json`.

```sh
DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_GENERATE_ASPNET_CERTIFICATE=false \
  dotnet restore tools/lubelogger-extract/LubeLoggerExtract.csproj --locked-mode
DOTNET_CLI_TELEMETRY_OPTOUT=1 \
  dotnet build tools/lubelogger-extract/LubeLoggerExtract.csproj -c Release --no-restore
dotnet tools/lubelogger-extract/bin/Release/net9.0/lubelogger-extract.dll --self-test
```

The self-test uses synthetic data to verify recovery of records still in the WAL, exact decimals, UTC dates, integer IDs, unknown nested fields, references, binary values, unchanged source hashes, private output permissions, and safe failures for invalid input or existing output.

## Extract

```sh
umask 077
dotnet tools/lubelogger-extract/bin/Release/net9.0/lubelogger-extract.dll \
  --input /private/snapshot/cartracker.db \
  --output /private/export/new-export.json
```

Both paths are required. The output directory must exist. Existing output files and database/WAL collisions are rejected. A temporary file is flushed and atomically renamed without overwriting. Output permissions are `0600`; temporary recovery directories are `0700`. Failed exports do not publish an output file. Temporary files are removed during normal cleanup; after a crash, inspect and remove abandoned private recovery directories yourself.

The output is `{ "formatVersion": 1, "collections": { "collectionName": [ /* documents */ ] } }`. Collections and document IDs are sorted. Documents use the official LiteDB extended JSON serializer, including `_id`, `$numberDecimal`, `$numberLong`, `$date`, and unknown fields. Dates retain the stored UTC instant, not an assumed local calendar date. Interpret units, currency, and date semantics separately using captured source configuration.

Every collection is exported, including any security collections. Keep the export private with the original backup. It is not safe to publish, attach to an issue, or upload as a CI artifact. The PitPilot importer must explicitly select supported domain collections and exclude authentication material. This helper does not import data or copy external assets. Missing assets and unsupported domain data must be resolved by migration validation.

The tool makes no network calls or telemetry exports. Standard output contains collection counts only, and failures omit database exception details that could contain private data.

## macOS isolation

`macos-extract.sb` denies network access and limits file reads to operating system files, the runtime, compiled tool, explicit database/WAL paths, and a private workspace. Writes are allowed only inside that workspace and `/dev/null`. Use canonical absolute paths for all parameters; set `TMPDIR` inside the workspace so recovery never writes to the system temporary directory. `SDK_ROOT` is the resolved .NET installation directory, `TOOL_ROOT` is the compiled output directory, and `EXTRACT_PROFILE` is the absolute path to `macos-extract.sb`.

```sh
umask 077
mkdir -p "$EXTRACT_WORK/tmp"
cd "$EXTRACT_WORK"
TMPDIR="$EXTRACT_WORK/tmp" DOTNET_EnableDiagnostics=0 DOTNET_CLI_TELEMETRY_OPTOUT=1 \
  sandbox-exec \
    -D SDK_ROOT="$EXTRACT_SDK" -D TOOL_ROOT="$EXTRACT_TOOL" \
    -D INPUT_DB="$EXTRACT_DB" -D INPUT_WAL="$EXTRACT_WAL" \
    -D WORK_DIR="$EXTRACT_WORK" \
    -f "$EXTRACT_PROFILE" \
    "$EXTRACT_SDK/dotnet" "$EXTRACT_TOOL/lubelogger-extract.dll" \
    --input "$EXTRACT_DB" --output "$EXTRACT_WORK/export.json"
```

First run `--self-test` under the same profile. Independently prove outbound connections fail, the source is not writable, and unrelated private files are unreadable before running against real data. This profile is macOS-specific; on Linux provide equivalent filesystem and network isolation before processing private snapshots.

## Dependency

[LiteDB 5.0.17](https://www.nuget.org/packages/LiteDB/5.0.17) is distributed under the MIT license, copyright 2014-2022 Mauricio David. Its original license is included in `THIRD-PARTY-NOTICES.txt`. See the upstream [BSON type documentation](https://www.litedb.org/docs/data-structure/) and [UTC pragma documentation](https://www.litedb.org/docs/pragmas/).
