# SKILLS.md

Repeatable tasks, with the commands and what done looks like.

## Add a provider event type
1. Add the constant to `internal/shop/shop.go` and, for the simulation, emit it in `World.buy`.
2. `AccessUntil` decides what the new state grants; add a case to `TestAccessUntil`.
3. `make study readme`. Done when the gateway's wrong denials and leaked euros did not get worse.

## Add a design step
1. Add a flag to `ingest.Design` and its behaviour to `Processor`.
2. Add a row to `lab.Designs` and a line to `adds` in `cmd/shop/readme.go`.
3. `make study readme`. The new row shows the paired sign test against the gateway; keep a step only if it moves a
   number the customer or finance sees.

## Change the access policy
Edit `grace_days` or `renewal_buffer_hours` in `config/shop.json`, then `make study readme`. The world's contract
(`contract_grace_days` in `config/world.json`) is the customer's promise and does not move with the product.

## Teach the support agent a request
1. Add the intent and its words to `support.Rules`, and a handler to `Resolver` that reads the providers first.
2. Add the request to `world/tickets.go` with its right action, and a case to `support_test.go`.
3. Done when `make study readme` shows 0 wrong refunds or grants for the agent with checks.

## Replace the reader with a language model
Implement `support.Reader` (return intent, customer number, email and code; never an action). The resolver does not
change. Score it with `make study` on dev only; the held-out phrasings are spent.

## Benchmark the MySQL ledger
`make bench DSN='user:pass@tcp(host:3306)/db'` (empties the tables). Done when `same_answers_as_memory` and
`same_ledger_as_memory` are true.

## Run the held-out world
Once, at the end: `make heldout readme`. Never re-run it for a better number; see docs/heldout.md.
