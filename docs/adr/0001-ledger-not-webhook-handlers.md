# 1. An entitlement ledger, not webhook handlers that flip flags

## Context
Access was decided by handlers that set `has_access = true` on `invoice.paid` and extended a date by one period.
Providers deliver at least once, out of order, late during outages and occasionally never. Duplicates added free
months; a late older event re-opened refunded subscriptions; a customer back from checkout waited for a webhook.

## Decision
Store the provider's latest snapshot of every subscription and pass, plus an append-only history of every state and
why it was applied. Access is a pure function of the snapshot and the policy (`shop.AccessUntil`), computed when the
snapshot is written and indexed as `(user_id, access_until)`, so the page check is one indexed lookup.

## Consequences
Re-applying any snapshot is harmless; support and reconciliation write through the same path as webhooks, and every
change carries its source. The design ladder in the README measures each step: duplicates cost €19k of free access
in the dev world; the ledger with ordering, read-back and reconcile gives away €0.
