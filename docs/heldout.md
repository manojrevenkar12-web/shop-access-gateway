# The held-out world

`config/heldout.json` keeps the dev world's catalogue, contract and calendar and changes the rest: seed 913 (other
customers, deliveries and tickets), 45% of customers on Chargebee instead of 30%, slower and more duplicated
deliveries (7% duplicates, 0.8% lost), a five-hour Chargebee outage on day 58, and support tickets written from a
second set of phrasings in English and German that the reader's keywords were never tuned on.

Every product choice was fixed on dev first: the designs, the renewal buffer, the code normalisation, the reader,
the resolver's rules, the VAT fallback and the gate thresholds. `make heldout` ran the study once and wrote
`reports/heldout.lock`; `cmd/shop study --world heldout` refuses to run while the lock exists. The numbers in the
README are what came out, including anything that got worse.

## What it showed

- Access: 0.14 wrong denials per 1,000 page checks (dev 0.46), one customer wrongly locked out, €0 given away.
- VAT: 4 errors in 4,000 checkouts not held for confirmation (dev 3); the gate allows 1 per 1,000.
- Support: the gate fails. The reader closed 492 tickets without a person (dev 1,239) and made 13 wrong refunds:
  12 instructions phrased in ways the injection check did not know, inside the withdrawal period, and 1 duplicate-
  charge refund on a ticket whose right answer was "one charge". The proposed fix (a person decides any refund the
  message did not ask for in plain words) is recorded here and not applied, because it would be tuned on these
  tickets; it needs a new held-out world to be measured.
