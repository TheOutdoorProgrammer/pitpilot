# 13. Separate bounded dashboard previews from averaged and recorded history

Date: 2026-10-11

## Status

Accepted.

## Context and Problem Statement

Short drives occupy almost no horizontal space in a month-long chart. Rendering first, minimum, maximum and last values in each reduced bucket makes vertical spikes dominate. Dashboard cards each fetch full detail history and repeatedly serialize the phone cache.

## Considered Options

1. Batch bounded recent previews and expose time averages separately from recorded history
2. Only reduce Swift chart marks after downloading full histories
3. Replace stored readings with permanent rollups or interpolate across unobserved gaps

## Decision Outcome

Provide one preview request for up to 32 visible metrics, with at most 10 values per metric and bounded indexed recent reads. Each preview explicitly selects the latest source/statistic/quality series. Engine previews use the latest observed session, separated by a 15-minute gap; slow metrics use a recent trend. Truncated previews are labeled. Numeric detail defaults to 48 time buckets and one observation-weighted mean per bucket. Readings retains the existing adaptive reduction and actual extrema. Preview and detail caches are independent; the dashboard never loads full history. Boolean/category/count semantics remain distinct. All raw readings and their provenance remain unchanged.

## Consequences

### Good

- Dashboard network, rendering and cache-write work is bounded.
- Recent session defaults make short drives readable without stretching actual timestamps.
- Users can switch to recorded extrema and retain source provenance and offline cached views.

### Bad

- Sample means hide within-bucket variability until the user selects Readings; they are not duration-weighted driving averages.
- The latest-series preview omits competing sources and older sessions that remain available in detail.
- A 512-observation preview cap can show only the tail of a long session and must disclose truncation.

### Rejected because

- Client-only decimation leaves unnecessary network, decoding and disk-cache work on the phone.
- Permanent rollups destroy detail needed for later inspection; interpolating missing intervals invents coverage.

