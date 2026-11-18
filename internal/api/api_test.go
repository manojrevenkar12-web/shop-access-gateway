package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/api"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/ingest"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/support"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/vat"
)

var now = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

type noAPI struct{}

func (noAPI) Get(context.Context, shop.Provider, string) (shop.Sub, bool, error) {
	return shop.Sub{}, false, nil
}
func (noAPI) List(context.Context, shop.Provider) ([]shop.Sub, error) { return nil, nil }

type vies struct{}

func (vies) Valid(context.Context, string) (bool, error) { return true, nil }

func server() (*api.Server, store.Store) {
	st := store.NewMemory()
	p := &ingest.Processor{Store: st, API: noAPI{}, Policy: shop.Policy{Grace: 7 * 24 * time.Hour},
		Design: ingest.Design{Dedupe: true, Order: true}}
	return &api.Server{Store: st, Processor: p,
		Resolver: &support.Resolver{Reader: support.Rules{}, Ledger: st, Processor: p, Automate: false},
		VAT:      vat.Rules{Home: "DE", Rates: map[string]float64{"DE": 19, "AT": 20}, EU: []string{"DE", "AT"}},
		VIES:     vies{}, Plans: map[string]shop.Plan{"monthly": {ID: "monthly", PriceCents: 3900}},
		Secrets: api.Secrets{StripeSigning: "whsec_test", StripeTolerance: 5 * time.Minute, ChargebeeUser: "cb", ChargebeePassword: "pw"},
		Now:     func() time.Time { return now }}, st
}

func eventBody(t *testing.T, id string, p shop.Provider, version int64) []byte {
	t.Helper()
	e := shop.Event{ID: id, Type: shop.CheckoutCompleted, Created: now.Add(-time.Minute), Object: shop.Sub{
		ID: "obj_1", Provider: p, User: 42, Plan: "monthly", Status: shop.Active, StartedAt: now.Add(-time.Minute),
		PeriodStart: now.Add(-time.Minute), PeriodEnd: now.Add(30 * 24 * time.Hour), Version: version}}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStripeSignature(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	h := api.SignStripe("whsec_test", now, body)
	if err := api.VerifyStripe("whsec_test", h, body, now.Add(time.Minute), 5*time.Minute); err != nil {
		t.Fatalf("valid signature refused: %v", err)
	}
	for name, tc := range map[string]struct {
		secret, header string
		body           []byte
		at             time.Time
	}{
		"wrong secret":   {"whsec_other", h, body, now},
		"changed body":   {"whsec_test", h, []byte(`{"id":"evt_2"}`), now},
		"replayed later": {"whsec_test", h, body, now.Add(10 * time.Minute)},
		"no header":      {"whsec_test", "", body, now},
		"garbage":        {"whsec_test", "t=abc,v1=zz", body, now},
	} {
		if err := api.VerifyStripe(tc.secret, tc.header, tc.body, tc.at, 5*time.Minute); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A rotated secret: Stripe sends several v1 signatures; any match is enough.
	two := h + ",v1=" + strings.Repeat("0", 64)
	if err := api.VerifyStripe("whsec_test", two, body, now, 5*time.Minute); err != nil {
		t.Fatalf("one of two signatures matching: %v", err)
	}
}

func TestWebhooksThenAccess(t *testing.T) {
	srv, _ := server()
	h := srv.Handler()
	body := eventBody(t, "evt_1", shop.Stripe, 0)
	post := func(path string, b []byte, set func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		set(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	signed := func(r *http.Request) { r.Header.Set("Stripe-Signature", api.SignStripe("whsec_test", now, body)) }
	if rec := post("/webhooks/stripe", body, func(*http.Request) {}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unsigned webhook: %d", rec.Code)
	}
	for i, want := range []string{"applied", "duplicate"} {
		rec := post("/webhooks/stripe", body, signed)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("delivery %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	cb := eventBody(t, "evt_cb", shop.Chargebee, 3)
	if rec := post("/webhooks/chargebee", cb, func(r *http.Request) { r.SetBasicAuth("cb", "wrong") }); rec.Code != http.StatusUnauthorized {
		t.Fatalf("chargebee bad password: %d", rec.Code)
	}
	if rec := post("/webhooks/chargebee", cb, func(r *http.Request) { r.SetBasicAuth("cb", "pw") }); rec.Code != http.StatusOK {
		t.Fatalf("chargebee: %d %s", rec.Code, rec.Body)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/access/42", nil))
	var a api.Access
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil || !a.Granted || len(a.Reasons) == 0 || a.Reasons[0].Describe != "paid" {
		t.Fatalf("access: %s", rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/access/nobody", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad user id: %d", rec.Code)
	}
}

func TestCampusQuoteAndGraphQL(t *testing.T) {
	srv, st := server()
	ctx := context.Background()
	_ = st.PutLicence(ctx, store.Licence{ID: "lic_1", Code: "charite-2026", Seats: 1, Starts: now.Add(-time.Hour),
		Ends: now.Add(24 * time.Hour), Customer: "charite"})
	h := srv.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	if rec := do("POST", "/v1/campus/join", `{"code":"Charite 2026","user":5}`); rec.Code != 200 {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/v1/campus/join", `{"code":"charite-2026","user":6}`); rec.Code != http.StatusConflict {
		t.Fatalf("full licence: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/v1/campus/join", `{"code":"x","user":6,"admin":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	rec := do("POST", "/v1/vat/quote", `{"plan":"monthly","billing":"AT","ip_country":"AT"}`)
	if !strings.Contains(rec.Body.String(), `"vat_cents":780`) {
		t.Fatalf("quote: %s", rec.Body)
	}
	rec = do("POST", "/graphql", `{"query":"{ access(user: 5) { granted reasons { source grants } } vatQuote(plan: \"monthly\", billing: \"AT\", ipCountry: \"AT\") { country vatCents ratePercent } }"}`)
	var out struct {
		Data struct {
			Access   struct{ Granted bool }
			VATQuote struct {
				Country  string
				VatCents int
			} `json:"vatQuote"`
		}
		Errors []any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Errors) > 0 || !out.Data.Access.Granted ||
		out.Data.VATQuote.Country != "AT" || out.Data.VATQuote.VatCents != 780 {
		t.Fatalf("graphql: %s", rec.Body)
	}
	rec = do("POST", "/graphql", `{"query":"mutation { joinCampus(code: \"charite-2026\", user: 9) { licence } }"}`)
	if !strings.Contains(rec.Body.String(), "every seat") {
		t.Fatalf("graphql full licence should error: %s", rec.Body)
	}
}

func TestSupportTicketGoesToAPersonWhenNotAutomated(t *testing.T) {
	srv, _ := server()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/support/tickets", strings.NewReader(`{"id":"T-1","text":"I paid AMB-1042"}`)))
	if !strings.Contains(rec.Body.String(), `"action":"escalate"`) {
		t.Fatalf("ticket: %s", rec.Body)
	}
}
