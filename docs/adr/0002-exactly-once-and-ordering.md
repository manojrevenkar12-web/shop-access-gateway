# 2. Exactly once by transaction, order by version

## Context
Stripe sends no version on its objects; Chargebee sends `resource_version`. Both retry until they see a 2xx.

## Decision
`INSERT IGNORE` the event id into `processed_events` in the same transaction as the ledger write; a duplicate does
nothing and answers 200. Lock the subscription row (`SELECT … FOR UPDATE`) and refuse a snapshot that is not newer:
Chargebee by `resource_version`, Stripe by the event's creation time, or by the read time when the gateway reads the
object back (on return, from support, in reconcile). A refused stale event stays marked processed. Deadlocks and lock
timeouts are retried; the transaction was rolled back, so that is safe.

## Rejected
Reading every Stripe object back on every event (the `gateway_refetch` ablation): no measurable gain in either world
and twice the provider API calls. It stays possible behind `Design.Refetch` for event types where it matters.
