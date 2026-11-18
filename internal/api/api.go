// Package api is the gateway's HTTP surface: the providers' webhooks (signed), the access check the platform asks
// on every page, the checkout return, campus joins, the support inbox and a GraphQL API over the same functions.
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/ingest"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/support"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/vat"
)

// Secrets authenticate the providers' webhooks.
type Secrets struct {
	StripeSigning     string        // whsec_…: HMAC-SHA256 key for the Stripe-Signature header
	StripeTolerance   time.Duration // reject signatures older than this (replays)
	ChargebeeUser     string        // Chargebee sends webhooks with HTTP basic auth
	ChargebeePassword string
}

// Server wires the gateway's parts to HTTP.
type Server struct {
	Store     store.Store
	Processor *ingest.Processor
	Resolver  *support.Resolver
	VAT       vat.Rules
	VIES      vat.Checker
	Plans     map[string]shop.Plan
	Secrets   Secrets
	Now       func() time.Time
	MaxBody   int64
	Report    func() ([]byte, error) // the latest study report, for the console
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	if s.MaxBody == 0 {
		s.MaxBody = 1 << 20
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/stripe", s.stripe)
	mux.HandleFunc("POST /webhooks/chargebee", s.chargebee)
	mux.HandleFunc("GET /v1/access/{user}", s.access)
	mux.HandleFunc("GET /v1/subscriptions/{id}/history", s.history)
	mux.HandleFunc("POST /v1/checkout/return", s.checkoutReturn)
	mux.HandleFunc("POST /v1/campus/join", s.join)
	mux.HandleFunc("POST /v1/support/tickets", s.ticket)
	mux.HandleFunc("POST /v1/vat/quote", s.quote)
	mux.HandleFunc("GET /v1/report", s.report)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.Handle("POST /graphql", s.GraphQL())
	return mux
}

// ---- webhooks ---------------------------------------------------------------------------------------------------

// SignStripe produces a Stripe-Signature header for a payload (used by tests and the replay tool).
func SignStripe(secret string, t time.Time, payload []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "%d.", t.Unix())
	m.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", t.Unix(), hex.EncodeToString(m.Sum(nil)))
}

var errSignature = errors.New("signature does not match")

// VerifyStripe checks a Stripe-Signature header: any v1 signature over "t.payload" that matches, within tolerance.
func VerifyStripe(secret, header string, payload []byte, now time.Time, tolerance time.Duration) error {
	var ts int64
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return errSignature
			}
			ts = n
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == 0 || len(sigs) == 0 {
		return errSignature
	}
	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("signature timestamp outside tolerance: %w", errSignature)
	}
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "%d.", ts)
	m.Write(payload)
	want := m.Sum(nil)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return errSignature
}

func (s *Server) stripe(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.MaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := VerifyStripe(s.Secrets.StripeSigning, r.Header.Get("Stripe-Signature"), body, s.Now(), s.Secrets.StripeTolerance); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.event(w, r.Context(), shop.Stripe, body)
}

func (s *Server) chargebee(w http.ResponseWriter, r *http.Request) {
	u, p, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.Secrets.ChargebeeUser)) != 1 ||
		subtle.ConstantTimeCompare([]byte(p), []byte(s.Secrets.ChargebeePassword)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="webhooks"`)
		fail(w, http.StatusUnauthorized, errors.New("bad credentials"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, s.MaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.event(w, r.Context(), shop.Chargebee, body)
}

// event applies one webhook. A duplicate or stale event answers 200 so the provider stops retrying; an error
// answers 500 so it retries — the ledger write and the processed mark are one transaction, so a retry is safe.
func (s *Server) event(w http.ResponseWriter, ctx context.Context, p shop.Provider, body []byte) {
	var e shop.Event
	if err := json.Unmarshal(body, &e); err != nil || e.ID == "" || e.Object.ID == "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("not an event: %v", err))
		return
	}
	e.Provider, e.Object.Provider = p, p
	out, err := s.Processor.Handle(ctx, e, s.Now())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, http.StatusOK, map[string]string{"event": e.ID, "outcome": string(out)})
}

// ---- access -----------------------------------------------------------------------------------------------------

// Access is the answer to "may this user in", with every source that was considered.
type Access struct {
	User    int64         `json:"user"`
	Granted bool          `json:"granted"`
	At      time.Time     `json:"at"`
	Reasons []shop.Reason `json:"reasons"`
}

// Explain answers an access check with its reasons.
func Explain(ctx context.Context, st store.Store, user int64, t time.Time) (Access, error) {
	a := Access{User: user, At: t, Reasons: []shop.Reason{}}
	g, err := st.Access(ctx, user, t)
	if err != nil {
		return a, err
	}
	a.Granted = g
	recs, err := st.SubsOfUser(ctx, user)
	if err != nil {
		return a, err
	}
	for _, r := range recs {
		grants := !t.Before(r.Sub.StartedAt) && t.Before(r.Until)
		a.Reasons = append(a.Reasons, shop.Reason{Source: string(r.Sub.Provider) + ":" + r.Sub.ID, Status: string(r.Sub.Status),
			Until: r.Until, Event: r.Source, Grants: grants, Describe: describe(r, grants)})
	}
	seats, err := st.SeatsOf(ctx, user)
	if err != nil {
		return a, err
	}
	for _, l := range seats {
		grants := !t.Before(l.Starts) && t.Before(l.Ends)
		a.Reasons = append(a.Reasons, shop.Reason{Source: "campus:" + l.Code, Status: "seat", Until: l.Ends,
			Event: "join", Grants: grants, Describe: fmt.Sprintf("campus seat on %s's licence", l.Customer)})
	}
	sort.SliceStable(a.Reasons, func(i, j int) bool { return a.Reasons[i].Grants && !a.Reasons[j].Grants })
	return a, nil
}

func describe(r store.Record, grants bool) string {
	switch {
	case grants && r.Sub.Status == shop.PastDue:
		return "renewal failed; access kept during the grace period while the provider retries"
	case grants && r.Sub.Status == shop.Trialing:
		return "trial"
	case grants && r.Sub.CancelAtPeriodEnd:
		return "cancelled; access runs to the end of the paid period"
	case grants:
		return "paid"
	case r.Sub.Status == shop.Expired:
		return "expired"
	case r.Sub.Status == shop.Canceled:
		return "cancelled"
	default:
		return "period over"
	}
}

func (s *Server) access(w http.ResponseWriter, r *http.Request) {
	user, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, errors.New("user must be a number"))
		return
	}
	a, err := Explain(r.Context(), s.Store, user, s.Now())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, http.StatusOK, a)
}

type historyEntry struct {
	Status      string    `json:"status"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Until       time.Time `json:"until"`
	Source      string    `json:"source"`
	At          time.Time `json:"at"`
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	recs, err := s.Store.History(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	out := []historyEntry{}
	for _, h := range recs {
		out = append(out, historyEntry{string(h.Sub.Status), h.Sub.PeriodStart, h.Sub.PeriodEnd, h.Until, h.Source, h.At})
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) checkoutReturn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider shop.Provider `json:"provider"`
		SubID    string        `json:"sub_id"`
		User     int64         `json:"user"`
	}
	if !decode(w, r, s.MaxBody, &in) {
		return
	}
	if in.Provider != shop.Stripe && in.Provider != shop.Chargebee {
		fail(w, http.StatusBadRequest, errors.New("provider must be stripe or chargebee"))
		return
	}
	changed, err := s.Processor.Return(r.Context(), in.Provider, in.SubID, s.Now())
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	a, err := Explain(r.Context(), s.Store, in.User, s.Now())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"updated": changed, "access": a})
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
		User int64  `json:"user"`
	}
	if !decode(w, r, s.MaxBody, &in) {
		return
	}
	l, err := s.Store.JoinCampus(r.Context(), support.NormaliseCode(in.Code), in.User, s.Now())
	switch {
	case errors.Is(err, store.ErrUnknownCode):
		fail(w, http.StatusNotFound, errors.New("no campus licence with that code; check the code from your university"))
	case errors.Is(err, store.ErrFull):
		fail(w, http.StatusConflict, errors.New("every seat on this licence is taken; your university can buy more"))
	case errors.Is(err, store.ErrNotActive):
		fail(w, http.StatusConflict, errors.New("this licence is not active today"))
	case err != nil:
		fail(w, http.StatusInternalServerError, err)
	default:
		reply(w, http.StatusOK, map[string]any{"licence": l.Code, "until": l.Ends})
	}
}

func (s *Server) ticket(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if !decode(w, r, s.MaxBody, &in) {
		return
	}
	if in.ID == "" || strings.TrimSpace(in.Text) == "" {
		fail(w, http.StatusBadRequest, errors.New("a ticket needs an id and text"))
		return
	}
	res, err := s.Resolver.Resolve(r.Context(), in.ID, in.Text, s.Now())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, http.StatusOK, res)
}

// QuoteRequest is a checkout to price.
type QuoteRequest struct {
	Plan    string `json:"plan"`
	Billing string `json:"billing"`
	IP      string `json:"ip_country"`
	Card    string `json:"card_country"`
	VATID   string `json:"vat_id"`
}

// Quote prices a checkout.
func (s *Server) Quote(ctx context.Context, q QuoteRequest) (vat.Quote, error) {
	p, ok := s.Plans[q.Plan]
	if !ok {
		return vat.Quote{}, fmt.Errorf("unknown plan %q", q.Plan)
	}
	return vat.Price(ctx, s.VAT, p.PriceCents, vat.Evidence{Billing: q.Billing, IP: q.IP, Card: q.Card, VATID: q.VATID}, s.VIES)
}

func (s *Server) quote(w http.ResponseWriter, r *http.Request) {
	var in QuoteRequest
	if !decode(w, r, s.MaxBody, &in) {
		return
	}
	q, err := s.Quote(r.Context(), in)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	reply(w, http.StatusOK, q)
}

func (s *Server) report(w http.ResponseWriter, _ *http.Request) {
	if s.Report == nil {
		fail(w, http.StatusNotFound, errors.New("no report: run `go run ./cmd/shop study`"))
		return
	}
	b, err := s.Report()
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// ---- helpers ----------------------------------------------------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, max int64, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, max))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("body: %w", err))
		return false
	}
	return true
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	reply(w, code, map[string]string{"error": err.Error()})
}
