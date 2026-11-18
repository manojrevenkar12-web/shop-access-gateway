package vat_test

import (
	"context"
	"testing"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/vat"
)

type vies map[string]bool

func (v vies) Valid(_ context.Context, id string) (bool, error) { return v[id], nil }

var rules = vat.Rules{Home: "DE", Rates: map[string]float64{"DE": 19, "AT": 20, "FR": 20, "US": 0},
	EU: []string{"DE", "AT", "FR"}}

func TestPrice(t *testing.T) {
	ctx := context.Background()
	v := vies{"FR123456789": true, "AT000000000": false}
	for _, tc := range []struct {
		name    string
		e       vat.Evidence
		country string
		vat     int64
		rc      bool
		review  bool
	}{
		{"two pieces agree", vat.Evidence{Billing: "AT", IP: "AT", Card: "DE"}, "AT", 780, false, false},
		{"billing missing, ip and card agree", vat.Evidence{IP: "fr", Card: "FR"}, "FR", 780, false, false},
		{"all differ: billing, kept for review", vat.Evidence{Billing: "AT", IP: "FR", Card: "DE"}, "AT", 780, false, true},
		{"valid VAT number elsewhere in the EU", vat.Evidence{Billing: "FR", IP: "FR", VATID: "FR123456789"}, "FR", 0, true, false},
		{"VAT number VIES does not confirm", vat.Evidence{Billing: "AT", IP: "AT", VATID: "AT000000000"}, "AT", 780, false, false},
		{"German business pays German VAT", vat.Evidence{Billing: "DE", IP: "DE", VATID: "DE999999999"}, "DE", 741, false, false},
		{"outside the EU", vat.Evidence{Billing: "US", IP: "US"}, "US", 0, false, false},
	} {
		q, err := vat.Price(ctx, rules, 3900, tc.e, v)
		if err != nil {
			t.Fatal(err)
		}
		review := q.Basis != "" && contains(q.Basis, "review")
		if q.Country != tc.country || q.VATCents != tc.vat || q.ReverseCharge != tc.rc || review != tc.review {
			t.Errorf("%s: got %+v", tc.name, q)
		}
		if q.GrossCents != q.NetCents+q.VATCents {
			t.Errorf("%s: gross %d != net %d + vat %d", tc.name, q.GrossCents, q.NetCents, q.VATCents)
		}
	}
}

func TestNaiveTrustsAnyVATNumberAndDefaultsHome(t *testing.T) {
	q := vat.Naive(rules, 3900, vat.Evidence{Billing: "AT", VATID: "AT000000000"})
	if !q.ReverseCharge {
		t.Fatal("the naive shop takes any VAT number at its word")
	}
	if q := vat.Naive(rules, 3900, vat.Evidence{IP: "FR", Card: "FR"}); q.Country != "DE" {
		t.Fatalf("missing billing defaults to home, got %s", q.Country)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
