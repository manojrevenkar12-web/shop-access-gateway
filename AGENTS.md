# AGENTS.md

How to work in this repository, for people and coding agents alike.

## Fast feedback

`make check` checks formatting, runs `go vet`, runs every test with the race detector and checks that the README
matches the reports: about 40 seconds. Set `SHOP_TEST_MYSQL_DSN=user:pass@tcp(127.0.0.1:3306)/shop` to run the store
contract, the seat race and the replay-equality test against MySQL too (CI does, on a `mysql:8.0` service). The full
dev study (`make study`) takes about 8 seconds.

## Layout

| Path | What it is | May import |
|---|---|---|
| `internal/shop` | the shared vocabulary: subscriptions, events, plans, the access policy | standard library |
| `internal/store` | the entitlement ledger: MySQL and memory behind one contract | shop |
| `internal/ingest` | webhooks into the ledger: de-duplication, ordering, read-back on return, nightly reconcile | shop, store |
| `internal/vat` | EU One-Stop-Shop VAT: residence from evidence, VIES, reverse charge | standard library |
| `internal/support` | the support agent: a reader proposes, the resolver checks the providers and decides | shop, store, ingest |
| `internal/api` | REST routes, signed webhooks, GraphQL | product |
| `internal/world` | the simulated providers, customers, deliveries, universities and the truth | shop |
| `internal/lab` | the bridge: plays the world through each design and scores it against the truth | everything |
| `cmd/shop` | `serve`, `study`, `bench`, `readme` | everything |
| `web/` | the React console | |

"product" means shop, store, ingest, vat, support and api.

## Invariants

1. **The truth stays in the world.** No product package imports `world` or `lab` (`TestInvariants` checks the
   imports). Whether a customer is truly entitled, the right answer to a ticket and a buyer's true country are
   used only by `lab` to score designs after the fact.
2. **The world does not read the shop's settings.** `world/` never opens `config/shop.json` and imports only
   `shop`. It keeps its own copy of the catalogue, the contract's grace period and the legal VAT rates.
3. **Same luck for every design.** Every draw is keyed by the seed and what it is about (`World.rng("deliver",
   eventID)`), never by call order, so every design sees the same deliveries, page visits and tickets; a customer's
   history does not change when the world grows (`TestKeyedLuck`, `TestSameLuckSameNumbers`).
4. **Every webhook is applied once and in order.** The processed mark and the ledger write are one transaction;
   an older snapshot is refused (Chargebee `resource_version`, Stripe event time or read time). A refused stale
   event stays processed, so a retry cannot apply it later.
5. **A seat is a row lock.** A campus join locks the licence row, counts and inserts in one transaction; 50
   concurrent joins on a 10-seat licence take 10 seats (`TestSeatsNeverOversold`, on MySQL in CI).
6. **The model reads and code decides.** The support reader (keyword rules today, a language model behind the same
   `Reader` interface later) only proposes an intent, a customer and a code. The resolver reads the providers and
   acts only on what they show; refunds carry an idempotency key; anything it cannot verify goes to a person.
   Ticket text is data: instructions inside it are never followed.
7. **Settings in config.** Plans, grace, the renewal buffer, the withdrawal period, VAT rates and the release gate
   live in `config/shop.json`; the world's parameters in `config/world.json`.
8. **Generated reports.** `reports/*.json` come from `cmd/shop study` and `cmd/shop bench`; the README dashboard and
   `docs/dashboard.svg` from `cmd/shop readme`. Never edit them by hand; `make check` fails when they are stale.

## The held-out rule

Every choice (the designs, the renewal buffer of 30 hours, the code normalisation, the reader's keywords, the
resolver's checks, the VAT fallback and the gate) was made on the dev world (seed 47). The held-out world
(`config/heldout.json`: seed 913, 45% Chargebee, more duplicates and losses, a Chargebee outage, ticket phrasings
the reader never saw) runs once with `make heldout`, which writes `reports/heldout.lock`; the command refuses to run
while the lock exists. Its numbers are reported as they came out. See docs/heldout.md.

## Conventions

- Go 1.24, standard library first; two dependencies (`go-sql-driver/mysql`, `graphql-go/graphql`).
- Times are UTC in the ledger; money is integer cents.
- Errors a customer can see say what happened and what to do next.
