// Package vat prices a checkout under EU VAT rules for digital services (the One-Stop-Shop scheme).
//
// The rate is the buyer's country of residence, which the shop must establish from two pieces of evidence that do
// not contradict each other (billing address, IP address, card country). A business buyer from another EU country
// with a VAT number that the EU's VIES service confirms pays no VAT (reverse charge); one that VIES does not
// confirm is a consumer.
package vat

import (
	"context"
	"math"
	"strings"
)

// Evidence is what the checkout knows about where the buyer lives.
type Evidence struct {
	Billing string // ISO country of the billing address, or empty
	IP      string
	Card    string
	VATID   string
}

// Checker asks VIES whether a VAT number is registered.
type Checker interface {
	Valid(ctx context.Context, vatID string) (bool, error)
}

// Rules are the shop's VAT settings (config/shop.json).
type Rules struct {
	Home  string             `json:"home"`
	Rates map[string]float64 `json:"rates"`
	EU    []string           `json:"eu"`
}

// Quote is a priced checkout and the reason for its rate.
type Quote struct {
	Country       string  `json:"country"`
	RatePercent   float64 `json:"rate_percent"`
	ReverseCharge bool    `json:"reverse_charge"`
	NetCents      int64   `json:"net_cents"`
	VATCents      int64   `json:"vat_cents"`
	GrossCents    int64   `json:"gross_cents"`
	Basis         string  `json:"basis"`
}

func (r Rules) eu(c string) bool {
	for _, x := range r.EU {
		if x == c {
			return true
		}
	}
	return false
}

// Residence picks the country two non-contradicting pieces of evidence agree on; with no agreement it falls back to
// the billing address, then the card, and says so in the basis.
func Residence(e Evidence) (string, string) {
	votes := map[string]int{}
	for _, c := range []string{e.Billing, e.IP, e.Card} {
		if c != "" {
			votes[strings.ToUpper(c)]++
		}
	}
	for c, n := range votes {
		if n >= 2 {
			return c, "two pieces of evidence agree"
		}
	}
	if e.Billing != "" {
		return strings.ToUpper(e.Billing), "billing address only: evidence conflicts, keep for review"
	}
	return strings.ToUpper(e.Card), "card country only: evidence conflicts, keep for review"
}

// Price quotes a net price for the evidence.
func Price(ctx context.Context, r Rules, net int64, e Evidence, vies Checker) (Quote, error) {
	country, basis := Residence(e)
	q := Quote{Country: country, NetCents: net, Basis: basis}
	if e.VATID != "" && r.eu(country) && country != r.Home {
		ok, err := vies.Valid(ctx, e.VATID)
		if err != nil {
			return q, err
		}
		if ok {
			q.ReverseCharge, q.Basis = true, "reverse charge: VAT number confirmed by VIES"
			q.GrossCents = net
			return q, nil
		}
		q.Basis = "VAT number not confirmed by VIES: priced as a consumer"
	}
	q.RatePercent = r.Rates[country]
	q.VATCents = int64(math.Round(float64(net) * q.RatePercent / 100))
	q.GrossCents = net + q.VATCents
	return q, nil
}

// Naive is what many shops do first: the billing country (or the home country when it is missing), and any VAT
// number taken at its word.
func Naive(r Rules, net int64, e Evidence) Quote {
	country := strings.ToUpper(e.Billing)
	if country == "" {
		country = r.Home
	}
	q := Quote{Country: country, NetCents: net, Basis: "billing country"}
	if e.VATID != "" && r.eu(country) && country != r.Home {
		q.ReverseCharge, q.GrossCents = true, net
		return q
	}
	q.RatePercent = r.Rates[country]
	q.VATCents = int64(math.Round(float64(net) * q.RatePercent / 100))
	q.GrossCents = net + q.VATCents
	return q
}
