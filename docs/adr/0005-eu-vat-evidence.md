# 5. VAT from two pieces of evidence and VIES

## Context
Digital services to EU consumers are taxed where the buyer lives (One-Stop-Shop). The shop must hold two pieces of
non-contradictory evidence (billing address, IP, card country). A business buyer from another EU country pays no
VAT only with a VAT number the EU's VIES service confirms.

## Decision
`vat.Residence` takes the country two pieces of evidence agree on; with no agreement it prices on the billing
address (or the card) and marks the checkout for the customer to confirm their country. Reverse charge only after
VIES confirms the number. Rates are in `config/shop.json`.

## Consequences
Dev: 15 errors in 4,000 checkouts instead of 155; 12 of the 15 are held for confirmation; the 3 silent ones are
buyers whose IP and card both point to the same wrong country, which the law accepts as evidence.
