// Package world simulates the shop's surroundings: customers who buy, renew, fail to pay, cancel and dispute;
// two payment providers whose webhooks arrive late, twice or never; and the truth of who should have access.
//
// The world is a pure function of its config: every draw is keyed by what it is about (a customer, an event), so
// every design of the shop sees the same customers, the same payments and the same webhook deliveries. Only the lab
// may import this package; nothing that serves does (a test checks).
package world

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
)

// Config is config/world.json (and heldout.json on top of it).
type Config struct {
	Seed      uint64    `json:"seed"`
	Start     time.Time `json:"start"`
	Days      int       `json:"days"`
	Customers int       `json:"customers"`
	Providers struct {
		ChargebeeShare float64 `json:"chargebee_share"`
	} `json:"providers"`
	Countries           map[string]float64 `json:"countries"`
	Plans               map[string]float64 `json:"plans"`
	TrialShare          float64            `json:"trial_share"`
	RenewalFailure      float64            `json:"renewal_failure"`
	DunningRetriesHours []int              `json:"dunning_retries_hours"`
	DunningRecovery     float64            `json:"dunning_recovery"`
	CancelPerPeriod     float64            `json:"cancel_per_period"`
	DisputeShare        float64            `json:"dispute_share"`
	WithdrawalShare     float64            `json:"withdrawal_share"`
	ContractGraceDays   int                `json:"contract_grace_days"`
	Delivery            Delivery           `json:"delivery"`
	SessionsPerDay      float64            `json:"sessions_per_day"`
	ReturnAfterSeconds  int                `json:"return_after_checkout_seconds"`
	FirstUseSeconds     int                `json:"first_use_after_checkout_seconds"`
	Campus              struct {
		Licences  int     `json:"licences"`
		Seats     []int   `json:"seats"`
		Members   int     `json:"members"`
		TypoShare float64 `json:"code_typo_share"`
	} `json:"campus"`
	Tickets  TicketConfig `json:"tickets"`
	VAT      VATConfig    `json:"vat"`
	Phrasing string       `json:"phrasing"`
}

type Delivery struct {
	FastShare       float64 `json:"fast_share"`
	FastMedianSec   float64 `json:"fast_median_seconds"`
	SlowShare       float64 `json:"slow_share"`
	SlowMaxMinutes  float64 `json:"slow_max_minutes"`
	LateMinHours    float64 `json:"late_min_hours"`
	LateMaxHours    float64 `json:"late_max_hours"`
	DuplicateShare  float64 `json:"duplicate_share"`
	DuplicateMaxMin float64 `json:"duplicate_max_minutes"`
	LostShare       float64 `json:"lost_share"`
	Outage          struct {
		Provider string `json:"provider"`
		Day      int    `json:"day"`
		Hours    int    `json:"hours"`
	} `json:"outage"`
}

// Load reads a world config; a file with "base"-less overrides (heldout.json) is merged onto world.json.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if filepath.Base(path) != "world.json" {
		base, err := os.ReadFile(filepath.Join(filepath.Dir(path), "world.json"))
		if err != nil {
			return Config{}, err
		}
		if err := json.Unmarshal(base, &cfg); err != nil {
			return Config{}, err
		}
	}
	err = json.Unmarshal(raw, &cfg)
	return cfg, err
}

// rng is a generator keyed by what the draws are about.
func (w *World) rng(parts ...any) *rand.Rand {
	h := fnv.New64a()
	fmt.Fprint(h, w.Cfg.Seed)
	for _, p := range parts {
		fmt.Fprint(h, "|", p)
	}
	s := h.Sum64()
	return rand.New(rand.NewPCG(s, s^0x9E3779B97F4A7C15))
}

func pick(r *rand.Rand, table map[string]float64) string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := 0.0
	for _, k := range keys {
		total += table[k]
	}
	x := r.Float64() * total
	for _, k := range keys {
		x -= table[k]
		if x < 0 {
			return k
		}
	}
	return keys[len(keys)-1]
}

// Customer is a person who buys access.
type Customer struct {
	ID       int64
	Email    string
	Country  string
	Provider shop.Provider
	Signup   time.Time
	Campus   string // licence code if a campus member
}

// TrueSub is a subscription or pass with its true event history.
type TrueSub struct {
	ID       string
	Provider shop.Provider
	User     int64
	Plan     string
	Events   []shop.Event // in the order the provider created them
	Charges  []Charge
}

// Charge is a payment the provider took.
type Charge struct {
	ID         string
	SubID      string
	User       int64
	Provider   shop.Provider
	AmountCent int64
	Created    time.Time
	Duplicate  bool // a second charge for the same purchase (a client retried without an idempotency key)
}

// Arrival is one webhook delivery.
type Arrival struct {
	At    time.Time
	Event shop.Event
	Dup   bool
}

// Session is a customer opening the platform; the gateway decides whether they get in.
type Session struct {
	At       time.Time
	User     int64
	FirstUse bool
}

// Return is a customer landing on the shop's success page after paying.
type Return struct {
	At       time.Time
	User     int64
	Provider shop.Provider
	SubID    string
}

// Licence is a campus licence; Join is a member typing its code.
type Licence struct {
	ID, Code, Customer string
	Seats              int
	Starts, Ends       time.Time
}
type Join struct {
	At    time.Time
	User  int64
	Typed string
}

// World is the simulated shop environment.
type World struct {
	Cfg       Config
	Customers []Customer
	Subs      []*TrueSub
	byID      map[string]*TrueSub
	byUser    map[int64][]*TrueSub
	Arrivals  []Arrival
	Sessions  []Session
	Returns   []Return
	Licences  []Licence
	Joins     []Join
	End       time.Time
	plans     map[string]planSpec
	campusAt  map[int64]time.Time // members entitled to a seat, from when they first tried to join
}

type planSpec struct {
	period    time.Duration
	recurring bool
	price     int64
}

var specs = map[string]planSpec{ // the provider's catalogue: what each plan bills
	"monthly":   {30 * 24 * time.Hour, true, 3900},
	"yearly":    {365 * 24 * time.Hour, true, 29900},
	"exam_pass": {30 * 24 * time.Hour, false, 4900},
}

// New builds the world from its config.
func New(cfg Config) *World {
	w := &World{Cfg: cfg, byID: map[string]*TrueSub{}, byUser: map[int64][]*TrueSub{}, plans: specs}
	w.End = cfg.Start.Add(time.Duration(cfg.Days) * 24 * time.Hour)
	span := float64(cfg.Days-5) * 24 * float64(time.Hour)
	for i := 0; i < cfg.Customers; i++ {
		r := w.rng("customer", i)
		c := Customer{ID: int64(100000 + i), Country: pick(r, cfg.Countries), Signup: cfg.Start.Add(time.Duration(r.Float64() * span)).Truncate(time.Second)}
		c.Email = fmt.Sprintf("customer%d@example.org", c.ID)
		c.Provider = shop.Stripe
		if r.Float64() < cfg.Providers.ChargebeeShare {
			c.Provider = shop.Chargebee
		}
		w.Customers = append(w.Customers, c)
		w.buy(c, pick(r, cfg.Plans), r)
	}
	w.campus()
	w.deliver()
	w.sessions()
	return w
}

func (w *World) buy(c Customer, plan string, r *rand.Rand) {
	spec := w.plans[plan]
	n := len(w.Subs)
	id := fmt.Sprintf("sub_%06d", n)
	if c.Provider == shop.Chargebee {
		id = fmt.Sprintf("cb_%06d", n)
	}
	s := &TrueSub{ID: id, Provider: c.Provider, User: c.ID, Plan: plan}
	t0 := c.Signup
	snap := shop.Sub{ID: id, Provider: c.Provider, User: c.ID, Plan: plan, StartedAt: t0, PeriodStart: t0}
	emit := func(typ string, at time.Time, x shop.Sub) {
		x.Version = int64(len(s.Events) + 1)
		if c.Provider == shop.Stripe {
			x.Version = 0 // Stripe sends no version
		}
		s.Events = append(s.Events, shop.Event{ID: fmt.Sprintf("evt_%s_%02d", id, len(s.Events)), Provider: c.Provider,
			Type: typ, Created: at, Object: x})
	}
	charge := func(at time.Time) {
		ch := Charge{ID: fmt.Sprintf("ch_%s_%02d", id, len(s.Charges)), SubID: id, User: c.ID, Provider: c.Provider,
			AmountCent: spec.price, Created: at}
		s.Charges = append(s.Charges, ch)
		if r.Float64() < w.Cfg.Tickets.DoubleChargeShare {
			dup := ch
			dup.ID += "_dup"
			dup.Created = at.Add(time.Duration(20+r.IntN(90)) * time.Second)
			dup.Duplicate = true
			s.Charges = append(s.Charges, dup)
		}
	}
	trial := spec.recurring && plan == "monthly" && r.Float64() < w.Cfg.TrialShare
	if trial {
		snap.Status, snap.PeriodEnd = shop.Trialing, t0.Add(5*24*time.Hour)
	} else {
		snap.Status, snap.PeriodEnd = shop.Active, t0.Add(spec.period)
		charge(t0)
	}
	emit(shop.CheckoutCompleted, t0, snap)
	if !spec.recurring {
		end := snap.PeriodEnd
		x := snap
		x.Status, x.EndedAt = shop.Expired, end
		emit(shop.SubUpdated, end, x)
	} else {
		grace := time.Duration(w.Cfg.ContractGraceDays) * 24 * time.Hour
	renewals:
		for snap.PeriodEnd.Before(w.End.Add(30 * 24 * time.Hour)) {
			if !trial && r.Float64() < w.Cfg.CancelPerPeriod {
				at := snap.PeriodStart.Add(time.Duration(r.Float64() * float64(snap.PeriodEnd.Sub(snap.PeriodStart))))
				snap.CancelAtPeriodEnd = true
				emit(shop.SubUpdated, at, snap)
				x := snap
				x.Status = shop.Canceled
				emit(shop.SubCanceled, snap.PeriodEnd, x)
				break
			}
			trial = false
			due := snap.PeriodEnd
			next := snap
			next.PeriodStart, next.PeriodEnd = due, due.Add(spec.period)
			if r.Float64() < w.Cfg.RenewalFailure {
				next.Status = shop.PastDue
				emit(shop.PaymentFailed, due, next)
				for _, h := range w.Cfg.DunningRetriesHours {
					if r.Float64() < w.Cfg.DunningRecovery {
						next.Status = shop.Active
						emit(shop.InvoicePaid, due.Add(time.Duration(h)*time.Hour), next)
						charge(due.Add(time.Duration(h) * time.Hour))
						snap = next
						continue renewals
					}
				}
				x := next
				x.Status, x.EndedAt = shop.Expired, due.Add(grace)
				emit(shop.SubUpdated, due.Add(grace), x)
				break
			}
			next.Status = shop.Active
			emit(shop.InvoicePaid, due, next)
			charge(due)
			snap = next
		}
	}
	// A refund (EU right of withdrawal, handled by people) or a dispute ends access early.
	cut := func(at time.Time, typ string) {
		var kept []shop.Event
		for _, e := range s.Events {
			if !e.Created.After(at) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			return
		}
		last := kept[len(kept)-1].Object
		if last.Status != shop.Active && last.Status != shop.Trialing && last.Status != shop.PastDue {
			return
		}
		s.Events = kept
		x := last
		x.Status, x.EndedAt = shop.Canceled, at
		emit(typ, at, x)
	}
	if r.Float64() < w.Cfg.WithdrawalShare {
		cut(t0.Add(time.Hour+time.Duration(r.Float64()*float64(13*24*time.Hour))).Truncate(time.Second), shop.ChargeRefunded)
	} else if r.Float64() < w.Cfg.DisputeShare {
		cut(t0.Add(3*24*time.Hour+time.Duration(r.Float64()*float64(37*24*time.Hour))).Truncate(time.Second), shop.DisputeCreated)
	}
	var evs []shop.Event
	for _, e := range s.Events {
		if e.Created.Before(w.End) {
			evs = append(evs, e)
		}
	}
	s.Events = evs
	w.Subs = append(w.Subs, s)
	w.byID[id] = s
	w.byUser[c.ID] = append(w.byUser[c.ID], s)
	w.Returns = append(w.Returns, Return{At: t0.Add(time.Duration(w.Cfg.ReturnAfterSeconds) * time.Second), User: c.ID,
		Provider: c.Provider, SubID: id})
}

func (w *World) campus() {
	cfg := w.Cfg.Campus
	names := []string{"charite", "lmu", "heidelberg", "tum-med", "hamburg", "koeln"}
	for i := 0; i < cfg.Licences; i++ {
		w.Licences = append(w.Licences, Licence{ID: fmt.Sprintf("lic_%d", i), Code: fmt.Sprintf("%s-2026", names[i]),
			Customer: names[i], Seats: cfg.Seats[i], Starts: w.Cfg.Start, Ends: w.End.Add(180 * 24 * time.Hour)})
	}
	for i := 0; i < cfg.Members; i++ {
		r := w.rng("campus", i)
		l := w.Licences[r.IntN(len(w.Licences))]
		c := Customer{ID: int64(900000 + i), Country: "DE", Provider: "", Campus: l.Code,
			Signup: w.Cfg.Start.Add(time.Duration(r.Float64() * float64(w.End.Sub(w.Cfg.Start)) * 0.9)).Truncate(time.Second)}
		c.Email = fmt.Sprintf("student%d@uni.example", c.ID)
		typed := l.Code
		if r.Float64() < cfg.TypoShare {
			typed = typo(l.Code, r)
		}
		w.Customers = append(w.Customers, c)
		w.Joins = append(w.Joins, Join{At: c.Signup, User: c.ID, Typed: typed})
	}
	sort.Slice(w.Joins, func(i, j int) bool { return w.Joins[i].At.Before(w.Joins[j].At) })
	// The licence agreement: a member is entitled from their first attempt, however they typed the code, while
	// seats remain.
	w.campusAt = map[int64]time.Time{}
	taken := map[string]int{}
	for _, j := range w.Joins {
		l := w.licence(w.Customers[w.Cfg.Customers+int(j.User-900000)].Campus)
		if taken[l.Code] < l.Seats {
			taken[l.Code]++
			w.campusAt[j.User] = j.At
		}
	}
}

func (w *World) licence(code string) Licence {
	for _, l := range w.Licences {
		if l.Code == code {
			return l
		}
	}
	return Licence{}
}

// typo is how people mistype a code: case, a missing hyphen, spaces.
func typo(code string, r *rand.Rand) string {
	switch r.IntN(3) {
	case 0:
		return "  " + upper(code) + " "
	case 1:
		out := []byte{}
		for i := 0; i < len(code); i++ {
			if code[i] != '-' {
				out = append(out, code[i])
			}
		}
		return string(out)
	default:
		return upper(code[:1]) + code[1:]
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func (w *World) deliver() {
	d := w.Cfg.Delivery
	for _, s := range w.Subs {
		for _, e := range s.Events {
			r := w.rng("deliver", e.ID)
			if r.Float64() < d.LostShare {
				continue
			}
			at := e.Created.Add(delay(r, d))
			if string(e.Provider) == d.Outage.Provider {
				from := w.Cfg.Start.Add(time.Duration(d.Outage.Day) * 24 * time.Hour)
				to := from.Add(time.Duration(d.Outage.Hours) * time.Hour)
				if !at.Before(from) && at.Before(to) {
					at = to.Add(time.Duration(r.Float64()*600) * time.Second) // the provider's queue drains
				}
			}
			if at.Before(w.End) {
				w.Arrivals = append(w.Arrivals, Arrival{At: at, Event: e})
			}
			if r.Float64() < d.DuplicateShare {
				dup := at.Add(time.Duration(r.Float64()*d.DuplicateMaxMin*60) * time.Second)
				if dup.Before(w.End) {
					w.Arrivals = append(w.Arrivals, Arrival{At: dup, Event: e, Dup: true})
				}
			}
		}
	}
	sort.SliceStable(w.Arrivals, func(i, j int) bool { return w.Arrivals[i].At.Before(w.Arrivals[j].At) })
}

func delay(r *rand.Rand, d Delivery) time.Duration {
	x := r.Float64()
	switch {
	case x < d.FastShare:
		return time.Duration(d.FastMedianSec * math.Exp(0.8*r.NormFloat64()) * float64(time.Second))
	case x < d.FastShare+d.SlowShare:
		return time.Duration((10 + r.Float64()*(d.SlowMaxMinutes*60-10)) * float64(time.Second))
	default:
		return time.Duration((d.LateMinHours + r.Float64()*(d.LateMaxHours-d.LateMinHours)) * float64(time.Hour))
	}
}

func (w *World) sessions() {
	for _, c := range w.Customers {
		r := w.rng("sessions", c.ID)
		for day := c.Signup.Truncate(24 * time.Hour).Add(24 * time.Hour); day.Before(w.End); day = day.Add(24 * time.Hour) {
			if r.Float64() < w.Cfg.SessionsPerDay {
				w.Sessions = append(w.Sessions, Session{At: day.Add(time.Duration(r.Float64() * float64(24*time.Hour))).Truncate(time.Second), User: c.ID})
			}
		}
	}
	for _, s := range w.Subs {
		w.Sessions = append(w.Sessions, Session{At: s.Events[0].Created.Add(time.Duration(w.Cfg.FirstUseSeconds) * time.Second), User: s.User, FirstUse: true})
	}
	for _, j := range w.Joins {
		w.Sessions = append(w.Sessions, Session{At: j.At.Add(time.Duration(w.Cfg.FirstUseSeconds) * time.Second), User: j.User, FirstUse: true})
	}
	sort.SliceStable(w.Sessions, func(i, j int) bool { return w.Sessions[i].At.Before(w.Sessions[j].At) })
}

// ---- the providers' APIs ---------------------------------------------------------------------------------------

// Get returns the provider's current snapshot of an object at time t, as its API would.
func (w *World) Get(p shop.Provider, id string, t time.Time) (shop.Sub, bool) {
	s := w.byID[id]
	if s == nil || s.Provider != p {
		return shop.Sub{}, false
	}
	var cur shop.Sub
	ok := false
	for _, e := range s.Events {
		if e.Created.After(t) {
			break
		}
		cur, ok = e.Object, true
	}
	return cur, ok
}

// List returns every object of a provider that exists at t.
func (w *World) List(p shop.Provider, t time.Time) []shop.Sub {
	var out []shop.Sub
	for _, s := range w.Subs {
		if s.Provider == p {
			if x, ok := w.Get(p, s.ID, t); ok {
				out = append(out, x)
			}
		}
	}
	return out
}

// Charges returns the charges the provider took for a customer up to t.
func (w *World) Charges(user int64, t time.Time) []Charge {
	var out []Charge
	for _, s := range w.byUser[user] {
		for _, c := range s.Charges {
			if !c.Created.After(t) {
				out = append(out, c)
			}
		}
	}
	return out
}

func (w *World) SubsOf(user int64) []*TrueSub { return w.byUser[user] }
func (w *World) SubByID(id string) *TrueSub   { return w.byID[id] }

// ---- the truth -------------------------------------------------------------------------------------------------

// Entitled is the contract: may this customer use the platform at t, given what really happened (every event in
// the order the provider created it). Written here independently of the shop's own rule.
func (w *World) Entitled(user int64, t time.Time) bool {
	if at, ok := w.campusAt[user]; ok && !t.Before(at) {
		return true
	}
	grace := time.Duration(w.Cfg.ContractGraceDays) * 24 * time.Hour
	for _, s := range w.byUser[user] {
		x, ok := w.Get(s.Provider, s.ID, t)
		if !ok || t.Before(x.StartedAt) {
			continue
		}
		var until time.Time
		switch x.Status {
		case shop.Trialing, shop.Active:
			until = x.PeriodEnd
		case shop.PastDue:
			until = x.PeriodStart.Add(grace)
		case shop.Canceled:
			until = x.PeriodEnd
			if !x.EndedAt.IsZero() {
				until = x.EndedAt
			}
		default:
			until = x.StartedAt
		}
		if t.Before(until) {
			return true
		}
	}
	return false
}

// EntitledUntil is the end of the access the final true state grants (for pricing access given away).
func (w *World) EntitledUntil(s *TrueSub) time.Time {
	grace := time.Duration(w.Cfg.ContractGraceDays) * 24 * time.Hour
	x, ok := w.Get(s.Provider, s.ID, w.End)
	if !ok {
		return time.Time{}
	}
	switch x.Status {
	case shop.Trialing, shop.Active:
		return x.PeriodEnd
	case shop.PastDue:
		return x.PeriodStart.Add(grace)
	case shop.Canceled:
		if !x.EndedAt.IsZero() {
			return x.EndedAt
		}
		return x.PeriodEnd
	default: // expired: access ran to the moment it expired
		if !x.EndedAt.IsZero() {
			return x.EndedAt
		}
		return x.StartedAt
	}
}

// DailyValueCents prices a day of a plan, from the provider's catalogue.
func DailyValueCents(plan string) float64 {
	s := specs[plan]
	return float64(s.price) / (s.period.Hours() / 24)
}

// IsMember reports whether a customer came through a campus licence.
func (w *World) IsMember(user int64) bool {
	i := int(user - 900000)
	return i >= 0 && i < w.Cfg.Campus.Members && w.Customers[w.Cfg.Customers+i].Campus != ""
}
