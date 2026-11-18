# The held-out world

`config/heldout.json` keeps the dev world's catalogue, contract and calendar and changes the rest: seed 913 (other
customers, deliveries and tickets), 45% of customers on Chargebee instead of 30%, slower and more duplicated
deliveries (7% duplicates, 0.8% lost), a five-hour Chargebee outage on day 58, and support tickets written from a
second set of phrasings in English and German that the reader's keywords were never tuned on.

Every product choice was fixed on dev first: the designs, the renewal buffer, the code normalisation, the reader,
the resolver's rules, the VAT fallback and the gate thresholds. `make heldout` ran the study once and wrote
`reports/heldout.lock`; `cmd/shop study --world heldout` refuses to run while the lock exists. The numbers in the
README are what came out, including anything that got worse.
