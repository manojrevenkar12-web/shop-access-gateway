# 4. The support agent reads the providers before it acts

## Context
"Paid but no access", "charged twice", "I want to withdraw", "cancel", "my campus code does not work" are most of
the billing inbox. An agent that does what the message says refunds people who are outside the withdrawal period,
refunds single charges and opens access for unpaid subscriptions; one message can also carry instructions.

## Decision
A reader proposes intent, customer number or email, and campus code (keyword rules in English and German today,
behind the `Reader` interface so a language model can replace it). The resolver decides from the providers only:
it syncs the provider state into the ledger before saying "you have access"; it refunds a duplicate only for two
charges of the same purchase within 15 minutes, with an idempotency key; it refunds a withdrawal only within 14
days of the first payment on a live subscription; it never follows instructions in the text; everything else goes
to a person with the evidence it found.

## Consequences
In the dev world the agent closes 1,239 of 1,380 tickets without a person and makes no wrong refund or grant; the
agent without checks makes 177. The ablation stays in the study so the cost of skipping the checks stays visible.
