# 3. Fulfil on return, reconcile nightly, bridge renewals

## Context
Most wrong denials came from three gaps: the seconds between paying and the webhook (first use), lost events, and
renewals whose webhook arrived after the period ended.

## Decision
- When the customer lands on the success page, read the checkout from the provider and write it (Stripe's own
  recommendation for fulfilment). First-use denials fell from 1,403 to 3 in the dev world.
- At 03:00 compare every object at both providers with the ledger and write where it differs.
- Keep an active, renewing subscription open for `renewal_buffer_hours` (30) past its period end while the renewal
  is on its way. Never for a pass, never for a subscription set to end; a failed renewal arrives as `past_due` and
  the contract's grace period applies.

## Consequences
The buffer is counted as a bridge, not a sale: leaked euros are computed from the paid-through state. 30 hours was
chosen on dev to cover the 03:00 reconcile after any renewal; the held-out world tests it with an outage.
