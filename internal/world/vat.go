package world

import (
	"fmt"
)

// VATConfig sets how messy the evidence of where a buyer lives is.
type VATConfig struct {
	Checkouts         int     `json:"checkouts"`
	BillingMissing    float64 `json:"billing_missing"`
	IPMismatch        float64 `json:"ip_mismatch"`
	CardMismatch      float64 `json:"card_mismatch"`
	B2BShare          float64 `json:"b2b_share"`
	InvalidVATIDShare float64 `json:"invalid_vat_id_share"`
}

// VATCase is one checkout: the evidence the shop sees, and where the buyer really lives.
type VATCase struct {
	ID           string
	Plan         string
	Billing      string // empty when the buyer skipped it
	IP           string
	Card         string
	VATID        string // empty for consumers
	TrueCountry  string
	TrueBusiness bool // the VAT ID is real and valid
}

var euCountries = []string{"DE", "AT", "FR", "NL", "ES", "IT", "PL"}

// VATCases are checkouts with the evidence a payment page collects.
func (w *World) VATCases() []VATCase {
	cfg := w.Cfg.VAT
	var out []VATCase
	for i := 0; i < cfg.Checkouts; i++ {
		r := w.rng("vat", i)
		home := pick(r, w.Cfg.Countries)
		other := func() string {
			for {
				c := pick(r, w.Cfg.Countries)
				if c != home {
					return c
				}
			}
		}
		c := VATCase{ID: fmt.Sprintf("co_%05d", i), Plan: []string{"monthly", "yearly", "exam_pass"}[r.IntN(3)],
			Billing: home, IP: home, Card: home, TrueCountry: home}
		if r.Float64() < cfg.BillingMissing {
			c.Billing = ""
		}
		if r.Float64() < cfg.IPMismatch {
			c.IP = other() // travelling, a VPN
		}
		if r.Float64() < cfg.CardMismatch {
			c.Card = other() // a card from another country
		}
		if isEU(home) && r.Float64() < cfg.B2BShare {
			c.VATID = fmt.Sprintf("%s%09d", home, r.IntN(1_000_000_000))
			c.TrueBusiness = r.Float64() >= cfg.InvalidVATIDShare
		}
		out = append(out, c)
	}
	return out
}

// ValidVATID answers like the EU's VIES service: is this VAT number registered.
func (w *World) ValidVATID(id string, cases map[string]bool) bool { return cases[id] }

func isEU(c string) bool {
	for _, x := range euCountries {
		if x == c {
			return true
		}
	}
	return false
}

// TrueVAT is what the law says for a case: the rate of the place of supply in percent, and whether the business
// buyer accounts for it (reverse charge). rates are the legal rates (the world's own copy).
func TrueVAT(c VATCase, rates map[string]float64, home string) (float64, bool) {
	if c.TrueBusiness && isEU(c.TrueCountry) && c.TrueCountry != home {
		return 0, true
	}
	return rates[c.TrueCountry], false
}

// LegalRates are the standard VAT rates for digital services in 2026.
var LegalRates = map[string]float64{"DE": 19, "AT": 20, "FR": 20, "NL": 21, "ES": 21, "IT": 22, "PL": 23, "GB": 20,
	"CH": 8.1, "US": 0}
