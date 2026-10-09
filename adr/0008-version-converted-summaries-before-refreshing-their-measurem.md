# 8. Version converted summaries before refreshing their measurements

Date: 2026-10-09

## Status

Accepted.

## Context and Problem Statement

LubeLogger continues updating generated daily notes after their original PitPilot conversion.
The migration ledger deliberately rejects changed notes once their visible projection has been removed.
Final migration must retain every original value while showing only the corrected current summary.

## Considered Options

1. Explicit preview and apply with append-only source and conversion versions
2. Import changed summaries under new identities
3. Overwrite the existing conversion and signals without retaining prior versions

## Decision Outcome

Chosen: **option 1**.

Keep the default conflict behavior. An explicit refresh-converted-summaries option uses the strict existing parser, validates the original conversion and every owned signal/context/batch row, archives the prior raw source and normalized measurements, then replaces only that conversion's rows in the import transaction. Preview binds the source and current archive state. Missing or modified rows, restored notes, vehicle changes and replacement key collisions remain conflicts. Export includes every prior version.

## Consequences

### Good

- Corrected summaries do not duplicate points in active charts.
- Original raw source, measurements and conversion provenance remain recoverable.
- Native edits and unrelated sources remain protected by transaction validation.

### Bad

- Retained versions increase database and export size.
- Prior measurements are recovered through archive export rather than plotted alongside current values.
- The source must remain available until a final quiesced snapshot reconciles successfully.

### Rejected because

- New identities duplicate daily points and obscure whether readings are corrections.
- Destructive overwrite loses the original measurements and prevents reconciliation audits.

