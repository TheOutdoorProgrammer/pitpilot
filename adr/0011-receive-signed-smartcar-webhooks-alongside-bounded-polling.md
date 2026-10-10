# 11. Receive signed Smartcar webhooks alongside bounded polling

Date: 2026-10-10

## Status

Accepted.

## Context and Problem Statement

Hourly Smartcar reads repeatedly return cached provider errors even after consent renewal. The owner requested webhook delivery to receive updates as the provider publishes them. PitPilot's vehicle API is private and existing signal storage already preserves OEM observation times and deduplicates readings.

## Considered Options

1. Add a dedicated signed webhook receiver with transactional receipts and retain hourly polling
2. Replace all polling with webhooks
3. Only enqueue a poll when a webhook arrives

## Decision Outcome

Chosen: **option 1**.

Expose only the exact webhook POST route through the existing public tunnel. Verify raw-body HMAC with the Application Management Token, pin the webhook ID, and match both provider vehicle and user to the encrypted local binding. Commit normalized measurements and hashed event receipts atomically before returning success. Reuse the signal adapter and original OEM times. Error deliveries have no trustworthy error-onset timestamp, so record their delivery metadata without treating them as fresh authentication failure or recovered measurements. Hourly polling continues as a bounded recovery fallback.

## Consequences

### Good

- Immediate native metric and last-known-location ingestion when Smartcar delivers valid updates.
- Retries survive process restarts without duplicating data or overwriting newer locations.
- The bearer-protected vehicle API remains private.

### Bad

- Requires an additional management-token secret and public callback configuration in Smartcar.
- Schema 9 adds durable receipt rows and requires a verified pre-upgrade backup for rollback.
- Webhook setup does not guarantee recovery of the provider's GM connection.

### Rejected because

- Replacing polling removes an independent recovery path while real provider webhook delivery is not yet verified.
- Enqueuing only a poll discards the supplied measurement payload and still depends on the stale snapshot endpoint.

