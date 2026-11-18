// Package lab runs the shop against the simulated world and scores it against the truth. It is the only package
// that imports both the world and the shop.
package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/ingest"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/support"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/vat"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

// ShopConfig is config/shop.json.
type ShopConfig struct {
	GraceDays          int `json:"grace_days"`
	RenewalBufferHours int `json:"renewal_buffer_hours"`
	Plans              map[string]struct {
		PeriodDays int   `json:"period_days"`
		PriceCents int64 `json:"price_cents"`
		Recurring  bool  `json:"recurring"`
		TrialDays  int   `json:"trial_days"`
	} `json:"plans"`
	ReconcileHour       int       `json:"reconcile_hour"`
	WithdrawalDays      int       `json:"withdrawal_days"`
	DoubleChargeMinutes int       `json:"double_charge_window_minutes"`
	VAT                 vat.Rules `json:"vat"`
	Gate                struct {
		MaxWrongDenials   float64 `json:"max_wrong_denials_per_1000_checks"`
		MaxLeakedEuros    float64 `json:"max_leaked_euros"`
		MaxWrongMoney     int     `json:"max_wrong_money_actions"`
		MaxVATErrorsPer1k float64 `json:"max_silent_vat_errors_per_1000_checkouts"`
	} `json:"gate"`
}

func LoadShop(path string) (ShopConfig, error) {
	var c ShopConfig
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	return c, err
}

func (c ShopConfig) Policy() shop.Policy {
	rec := map[string]bool{}
	for id, p := range c.Plans {
		rec[id] = p.Recurring
	}
	return shop.Policy{Grace: time.Duration(c.GraceDays) * 24 * time.Hour,
		RenewalBuffer: time.Duration(c.RenewalBufferHours) * time.Hour, Recurring: rec}
}

func (c ShopConfig) PlanMap() map[string]shop.Plan {
	out := map[string]shop.Plan{}
	for id, p := range c.Plans {
		out[id] = shop.Plan{ID: id, Period: time.Duration(p.PeriodDays) * 24 * time.Hour, PriceCents: p.PriceCents,
			Recurring: p.Recurring, TrialPeriod: time.Duration(p.TrialDays) * 24 * time.Hour}
	}
	return out
}

// Design is a whole shop design: how webhooks are applied, and how the gateway reads campus codes.
type Design struct {
	ingest.Design
	NormaliseCodes bool `json:"normalise_codes"`
}

// Designs compared on the same world.
var Designs = []Design{
	{Design: ingest.Design{Name: "naive", Accumulate: true}},
	{Design: ingest.Design{Name: "dedupe", Dedupe: true}, NormaliseCodes: true},
	{Design: ingest.Design{Name: "dedupe_order", Dedupe: true, Order: true}, NormaliseCodes: true},
	{Design: ingest.Design{Name: "plus_fulfil", Dedupe: true, Order: true, FulfilOnReturn: true}, NormaliseCodes: true},
	{Design: ingest.Design{Name: "plus_reconcile", Dedupe: true, Order: true, FulfilOnReturn: true, Reconcile: true}, NormaliseCodes: true},
	{Design: ingest.Design{Name: "gateway", Dedupe: true, Order: true, FulfilOnReturn: true, Reconcile: true, Buffer: true}, NormaliseCodes: true},
	{Design: ingest.Design{Name: "gateway_refetch", Dedupe: true, Order: true, FulfilOnReturn: true, Reconcile: true, Buffer: true, Refetch: true}, NormaliseCodes: true},
}

var f = fmt.Sprintf

// Proposed is the design the shop runs.
const Proposed = "gateway"

// api is the providers' APIs as the world answers them at the simulation's clock.
type api struct {
	w     *world.World
	clock func() time.Time
	calls *atomic.Int64
}

func (a api) Get(_ context.Context, p shop.Provider, id string) (shop.Sub, bool, error) {
	a.calls.Add(1)
	s, ok := a.w.Get(p, id, a.clock())
	return s, ok, nil
}

// List counts one call per page of 100 objects, as the providers page their list endpoints.
func (a api) List(_ context.Context, p shop.Provider) ([]shop.Sub, error) {
	out := a.w.List(p, a.clock())
	a.calls.Add(int64(1 + len(out)/100))
	return out, nil
}

// AccessResult scores one design over the whole history.
type AccessResult struct {
	Design            string  `json:"design"`
	Checks            int     `json:"checks"`
	WrongDenials      int     `json:"wrong_denials"`
	WrongDenialsPer1k float64 `json:"wrong_denials_per_1000"`
	FirstUseDenied    int     `json:"first_use_denied"`
	FirstUseChecks    int     `json:"first_use_checks"`
	WrongGrants       int     `json:"wrong_grants"`
	UsersDenied       int     `json:"users_wrongly_denied"`
	UsersGranted      int     `json:"users_wrongly_granted"`
	LeakedEuros       float64 `json:"leaked_euros"`
	WithheldEuros     float64 `json:"withheld_euros"`
	Deliveries        int     `json:"deliveries"`
	Duplicates        int     `json:"duplicates_refused"`
	Stale             int     `json:"stale_refused"`
	Repairs           int     `json:"reconcile_repairs"`
	EventsPerSecond   float64 `json:"events_per_second"`
	APICalls          int     `json:"provider_api_calls"`
	CampusDenials     int     `json:"campus_wrong_denials"`
	CampusGrants      int     `json:"campus_wrong_grants"`
	Answers           []bool  `json:"-"`
}

type step struct {
	at   time.Time
	kind int // 0 arrival, 1 return, 2 join, 3 session, 4 reconcile, 5 ticket
	i    int
}

// Timeline merges everything that happens, in order. Ties keep a fixed order: deliveries, returns, joins, sessions.
func Timeline(w *world.World, cfg ShopConfig, tickets []world.Ticket) []step {
	var out []step
	for i, a := range w.Arrivals {
		out = append(out, step{a.At, 0, i})
	}
	for i, r := range w.Returns {
		if r.At.Before(w.End) {
			out = append(out, step{r.At, 1, i})
		}
	}
	for i, j := range w.Joins {
		out = append(out, step{j.At, 2, i})
	}
	for i, s := range w.Sessions {
		if s.At.Before(w.End) {
			out = append(out, step{s.At, 3, i})
		}
	}
	for d := w.Cfg.Start.Add(time.Duration(cfg.ReconcileHour) * time.Hour); d.Before(w.End); d = d.Add(24 * time.Hour) {
		out = append(out, step{d, 4, 0})
	}
	for i, t := range tickets {
		if t.At.Before(w.End) {
			out = append(out, step{t.At, 5, i})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].at.Equal(out[j].at) {
			return out[i].kind < out[j].kind
		}
		return out[i].at.Before(out[j].at)
	})
	return out
}

// Run plays the world's history through one design on a store; with a resolver it also answers the tickets.
func Run(ctx context.Context, w *world.World, cfg ShopConfig, d Design, st store.Store, tickets []world.Ticket,
	res *support.Resolver, keepAnswers bool) (AccessResult, []support.Resolution, error) {
	now := w.Cfg.Start
	clock := func() time.Time { return now }
	var calls atomic.Int64
	proc := &ingest.Processor{Store: st, API: api{w, clock, &calls}, Policy: cfg.Policy(), Plans: cfg.PlanMap(), Design: d.Design}
	for _, l := range w.Licences {
		if err := st.PutLicence(ctx, store.Licence{ID: l.ID, Code: l.Code, Seats: l.Seats, Starts: l.Starts, Ends: l.Ends,
			Customer: l.Customer}); err != nil {
			return AccessResult{}, nil, err
		}
	}
	if res != nil {
		res.Processor, res.Ledger = proc, st
		if b, ok := res.Billing.(*Billing); ok {
			b.clock = clock
		}
	}
	r := AccessResult{Design: d.Name}
	denied, granted := map[int64]bool{}, map[int64]bool{}
	resolutions := make([]support.Resolution, len(tickets)) // aligned with tickets; Action "" = not reached in time
	start := time.Now()
	for _, s := range Timeline(w, cfg, tickets) {
		now = s.at
		switch s.kind {
		case 0:
			r.Deliveries++
			out, err := proc.Handle(ctx, w.Arrivals[s.i].Event, now)
			if err != nil {
				return r, nil, err
			}
			if out == ingest.Duplicate {
				r.Duplicates++
			} else if out == ingest.Stale {
				r.Stale++
			}
		case 1:
			rt := w.Returns[s.i]
			if _, err := proc.Return(ctx, rt.Provider, rt.SubID, now); err != nil {
				return r, nil, err
			}
		case 2:
			j := w.Joins[s.i]
			code := j.Typed
			if d.NormaliseCodes {
				code = support.NormaliseCode(code)
			}
			_, _ = st.JoinCampus(ctx, code, j.User, now) // a failed join is the customer's problem until support helps
		case 3:
			sess := w.Sessions[s.i]
			got, err := st.Access(ctx, sess.User, now)
			if err != nil {
				return r, nil, err
			}
			want := w.Entitled(sess.User, now)
			r.Checks++
			if keepAnswers {
				r.Answers = append(r.Answers, got == want)
			}
			if sess.FirstUse {
				r.FirstUseChecks++
			}
			switch {
			case want && !got:
				r.WrongDenials++
				if w.IsMember(sess.User) {
					r.CampusDenials++
				}
				denied[sess.User] = true
				if sess.FirstUse {
					r.FirstUseDenied++
				}
			case got && !want:
				r.WrongGrants++
				if w.IsMember(sess.User) {
					r.CampusGrants++
				}
				granted[sess.User] = true
			}
		case 4:
			rec, err := proc.Reconcile(ctx, now)
			if err != nil {
				return r, nil, err
			}
			r.Repairs += rec.Repaired
		case 5:
			if res != nil {
				t := tickets[s.i]
				out, err := res.Resolve(ctx, t.ID, t.Text, now)
				if err != nil {
					return r, nil, err
				}
				resolutions[s.i] = out
			}
		}
	}
	r.EventsPerSecond = float64(r.Deliveries) / time.Since(start).Seconds()
	r.UsersDenied, r.UsersGranted = len(denied), len(granted)
	r.APICalls = int(calls.Load())
	if r.Checks > 0 {
		r.WrongDenialsPer1k = 1000 * float64(r.WrongDenials) / float64(r.Checks)
	}
	// What the final ledger gives away, and what it withholds, inside the simulated period.
	for _, s := range w.Subs {
		rec, ok, err := st.Sub(ctx, s.ID)
		if err != nil {
			return r, nil, err
		}
		prod := time.Time{}
		if ok { // what the ledger believes was paid for; the renewal buffer is a bridge, not a sale
			pol := cfg.Policy()
			pol.RenewalBuffer = 0
			prod = shop.AccessUntil(rec.Sub, pol)
		}
		truth := w.EntitledUntil(s)
		clip := func(t time.Time) time.Time { // access already granted into the future counts; a sub the ledger
			if t.Before(w.Cfg.Start) { // never saw grants nothing
				return w.Cfg.Start
			}
			return t
		}
		daily := world.DailyValueCents(s.Plan) / 100
		if d := clip(prod).Sub(clip(truth)); d > 0 {
			r.LeakedEuros += d.Hours() / 24 * daily
		} else if d < 0 {
			r.WithheldEuros += -d.Hours() / 24 * daily
		}
	}
	return r, resolutions, nil
}

// ---- support ----------------------------------------------------------------------------------------------------

// Billing is the providers' billing APIs over the world, recording what support did.
type Billing struct {
	mu       sync.Mutex
	w        *world.World
	clock    func() time.Time
	refunds  map[string]string // idempotency key -> charge
	refunded map[string]int    // charge -> times refunded
	cancels  map[string]bool
}

func NewBilling(w *world.World) *Billing {
	return &Billing{w: w, refunds: map[string]string{}, refunded: map[string]int{}, cancels: map[string]bool{}}
}

func (b *Billing) SubsOf(_ context.Context, user int64) ([]shop.Sub, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []shop.Sub
	for _, s := range b.w.SubsOf(user) {
		if x, ok := b.w.Get(s.Provider, s.ID, b.clock()); ok {
			x.CancelAtPeriodEnd = x.CancelAtPeriodEnd || b.cancels[s.ID]
			out = append(out, x)
		}
	}
	return out, nil
}

func (b *Billing) Charges(_ context.Context, user int64) ([]support.Charge, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []support.Charge
	for _, c := range b.w.Charges(user, b.clock()) {
		out = append(out, support.Charge{ID: c.ID, SubID: c.SubID, Provider: c.Provider, Amount: c.AmountCent,
			Created: c.Created, Refunded: b.refunded[c.ID] > 0})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// Refund is idempotent per key, as the providers' APIs are: a retried refund refunds once.
func (b *Billing) Refund(_ context.Context, chargeID, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, done := b.refunds[key]; done {
		return nil
	}
	b.refunds[key] = chargeID
	b.refunded[chargeID]++
	return nil
}

func (b *Billing) CancelAtPeriodEnd(_ context.Context, subID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancels[subID] = true
	return nil
}

// Directory finds customers by email.
type Directory struct{ byEmail map[string]int64 }

func NewDirectory(w *world.World) Directory {
	d := Directory{byEmail: map[string]int64{}}
	for _, c := range w.Customers {
		d.byEmail[strings.ToLower(c.Email)] = c.ID
	}
	return d
}

func (d Directory) ByEmail(_ context.Context, email string) (int64, bool) {
	id, ok := d.byEmail[strings.ToLower(email)]
	return id, ok
}

// SupportResult scores a way of answering the inbox.
type SupportResult struct {
	Agent          string            `json:"agent"`
	Tickets        int               `json:"tickets"`
	Right          int               `json:"right"`
	Automated      int               `json:"automated"`
	AutomatedRight int               `json:"automated_right"`
	Escalated      int               `json:"escalated"`
	WrongMoney     int               `json:"wrong_money_or_access_actions"`
	DoubleRefunds  int               `json:"double_refunds"`
	PersonHours    float64           `json:"person_hours"`
	ByKind         map[string][2]int `json:"by_kind"` // kind -> [right, total]
	Confusion      map[string]int    `json:"wrong_actions"`
}

func ScoreSupport(agent string, tickets []world.Ticket, out []support.Resolution, b *Billing, minutes float64) SupportResult {
	r := SupportResult{Agent: agent, ByKind: map[string][2]int{}, Confusion: map[string]int{}}
	for i, res := range out {
		if res.Action == "" {
			continue
		}
		r.Tickets++
		t := tickets[i]
		k := r.ByKind[t.Kind]
		k[1]++
		if res.Action == "escalate" {
			r.Escalated++
			r.PersonHours += minutes / 60
		} else {
			r.Automated++
		}
		if res.Action == t.Truth {
			r.Right++
			k[0]++
			if res.Action != "escalate" {
				r.AutomatedRight++
			}
		} else {
			if support.MoneyOrAccess(res.Action) {
				r.WrongMoney++
			}
			r.Confusion[fmt.Sprintf("%s -> %s", t.Truth, res.Action)]++
		}
		r.ByKind[t.Kind] = k
	}
	if b != nil {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, n := range b.refunded {
			if n > 1 {
				r.DoubleRefunds++
			}
		}
	}
	return r
}

// ---- VAT --------------------------------------------------------------------------------------------------------

type vies map[string]bool

// VIES answers like the EU's VAT-number service for the world's business buyers.
func VIES(w *world.World) vat.Checker {
	v := vies{}
	for _, c := range w.VATCases() {
		if c.VATID != "" {
			v[c.VATID] = c.TrueBusiness
		}
	}
	return v
}

func (v vies) Valid(_ context.Context, id string) (bool, error) { return v[id], nil }

// VATResult scores a way of pricing checkouts.
type VATResult struct {
	Method     string  `json:"method"`
	Checkouts  int     `json:"checkouts"`
	Wrong      int     `json:"wrong"`
	Silent     int     `json:"wrong_and_not_kept_for_review"`
	WrongEuros float64 `json:"wrong_euros"`
	UnderEuros float64 `json:"under_collected_euros"`
	ForReview  int     `json:"kept_for_review"`
	WrongRC    int     `json:"wrong_reverse_charge"`
}

func ScoreVAT(ctx context.Context, w *world.World, cfg ShopConfig) ([]VATResult, error) {
	cases := w.VATCases()
	valid := VIES(w)
	prices := cfg.PlanMap()
	var naive, proposed = VATResult{Method: "billing country", Checkouts: len(cases)}, VATResult{Method: "evidence + VIES", Checkouts: len(cases)}
	for _, c := range cases {
		net := prices[c.Plan].PriceCents
		rate, rc := world.TrueVAT(c, world.LegalRates, cfg.VAT.Home)
		want := int64(0)
		if !rc {
			want = int64(float64(net)*rate/100 + 0.5)
		}
		ev := vat.Evidence{Billing: c.Billing, IP: c.IP, Card: c.Card, VATID: c.VATID}
		q, err := vat.Price(ctx, cfg.VAT, net, ev, valid)
		if err != nil {
			return nil, err
		}
		score := func(r *VATResult, q vat.Quote) {
			if q.VATCents != want || q.ReverseCharge != rc {
				r.Wrong++
				if !strings.Contains(q.Basis, "review") {
					r.Silent++
				}
				diff := float64(q.VATCents-want) / 100
				if diff < 0 {
					r.UnderEuros += -diff
					diff = -diff
				}
				r.WrongEuros += diff
				if q.ReverseCharge != rc {
					r.WrongRC++
				}
			}
		}
		score(&proposed, q)
		if strings.Contains(q.Basis, "review") {
			proposed.ForReview++
		}
		score(&naive, vat.Naive(cfg.VAT, net, ev))
	}
	return []VATResult{naive, proposed}, nil
}

// Root finds the repository root from the working directory.
func Root() string {
	dir, _ := os.Getwd()
	for d := dir; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return dir
}
