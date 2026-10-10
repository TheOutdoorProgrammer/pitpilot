| # | Decision | In one line |
| --- | --- | --- |
| [0001](0001-start-with-a-single-household-go-service-and-native-ios-clie.md) | Start with a single household Go service and native iOS client | Start with a single household Go service and native iOS client |
| [0002](0002-migrate-lubelogger-through-typed-snapshots-and-transactional.md) | Migrate LubeLogger through typed snapshots and transactional reconciliation | Preserve typed source data and reconcile migration atomically |
| [0003](0003-store-vehicle-signals-with-observation-provenance-and-preser.md) | Store vehicle signals with observation provenance and preserve summary conversions atomically | Native signals retain sample and aggregate semantics, with atomic source conversion and private scraping. |
| [0004](0004-use-scoped-collectors-and-application-owned-smartcar-connect.md) | Use scoped collectors and application-owned Smartcar connections | Revocable collector credentials, signed updates, and server-side Smartcar V3 grants. |
| [0005](0005-derive-odometers-from-measured-baselines-and-immutable-dista.md) | Derive odometers from measured baselines and immutable distance intervals | Derive odometers from measured baselines and immutable distance intervals |
| [0006](0006-derive-automatic-trips-from-scoped-durable-gps-fixes.md) | Derive automatic trips from scoped durable GPS fixes | Opt-in actual USB GPS fixes, durable scoped delivery, replay-safe deletion and backend trip projection. |
| [0007](0007-keep-bounded-vehicle-photos-inside-the-garage-database.md) | Keep bounded vehicle photos inside the garage database | Keep bounded vehicle photos inside the garage database |
| [0008](0008-version-converted-summaries-before-refreshing-their-measurem.md) | Version converted summaries before refreshing their measurements | Version converted summaries before refreshing their measurements |
| [0009](0009-recover-receiver-history-through-immutable-event-archives-an.md) | Recover receiver history through immutable event archives and transactional reconciliation | Import genuine timed readings with replay-safe archives and explicit overlap reconciliation. |
| [0010](0010-discard-legacy-telemetry-without-deleting-live-readings.md) | Discard legacy telemetry without deleting live readings | Delete only imported telemetry with proof of ownership and prevent its replay while preserving live data. |
| [0011](0011-receive-signed-smartcar-webhooks-alongside-bounded-polling.md) | Receive signed Smartcar webhooks alongside bounded polling | Receive signed Smartcar webhooks alongside bounded polling |
