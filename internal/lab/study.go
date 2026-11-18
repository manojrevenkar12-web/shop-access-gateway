package lab

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/support"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

// Report is what one world says about every design. It is written to reports/<world>.json and the README is
// generated from it.
type Report struct {
	World      string          `json:"world"`
	Seed       uint64          `json:"seed"`
	Days       int             `json:"days"`
	Customers  int             `json:"customers"`
	Subs       int             `json:"subscriptions"`
	Deliveries int             `json:"webhook_deliveries"`
	Duplicated int             `json:"duplicated_deliveries"`
	Lost       int             `json:"lost_events"`
	OutOfOrder int             `json:"out_of_order_deliveries"`
	Members    int             `json:"campus_members"`
	Tickets    int             `json:"tickets"`
	PersonMin  float64         `json:"person_minutes_per_ticket"`
	Chargebee  float64         `json:"chargebee_share"`
	Access     []AccessResult  `json:"access"`
	Paired     []Paired        `json:"paired_vs_proposed"`
	Support    []SupportResult `json:"support"`
	VAT        []VATResult     `json:"vat"`
	Gate       Gate            `json:"gate"`
	Took       string          `json:"took"`
}

// Gate is the release decision for the proposed design on this world.
type Gate struct {
	Design string   `json:"design"`
	Pass   bool     `json:"pass"`
	Checks []string `json:"checks"`
}

// Study runs every design, the three ways of answering the inbox and both ways of pricing VAT on one world.
func Study(ctx context.Context, name string, wc world.Config, cfg ShopConfig) (Report, error) {
	t0 := time.Now()
	w := world.New(wc)
	tickets := w.Tickets()
	rep := Report{World: name, Seed: wc.Seed, Days: wc.Days, Customers: len(w.Customers), Subs: len(w.Subs),
		Deliveries: len(w.Arrivals), Members: wc.Campus.Members, Tickets: len(tickets),
		PersonMin: wc.Tickets.PersonMinutes, Chargebee: wc.Providers.ChargebeeShare}
	events := 0
	created := map[string]time.Time{}
	for _, s := range w.Subs {
		events += len(s.Events)
		for _, e := range s.Events {
			created[e.ID] = e.Created
		}
	}
	seen := map[string]bool{}
	latest := map[string]time.Time{} // per object: created time of the newest event delivered so far
	for _, a := range w.Arrivals {
		if a.Dup {
			rep.Duplicated++
		}
		if !seen[a.Event.ID] {
			seen[a.Event.ID] = true
		}
		obj := a.Event.Object.ID
		if latest[obj].After(a.Event.Created) {
			rep.OutOfOrder++
		} else {
			latest[obj] = a.Event.Created
		}
	}
	rep.Lost = events - len(seen)

	for _, d := range Designs {
		r, _, err := Run(ctx, w, cfg, d, store.NewMemory(), nil, nil, true)
		if err != nil {
			return rep, err
		}
		rep.Access = append(rep.Access, r)
	}
	var base []bool
	for _, a := range rep.Access {
		if a.Design == Proposed {
			base = a.Answers
		}
	}
	for _, a := range rep.Access {
		if a.Design == Proposed {
			continue
		}
		p := Paired{A: a.Design, B: Proposed}
		for i := range base {
			if !a.Answers[i] && base[i] {
				p.OnlyAWrong++
			} else if a.Answers[i] && !base[i] {
				p.OnlyBWrong++
			}
		}
		p.SignTestP = SignTest(p.OnlyAWrong, p.OnlyBWrong)
		rep.Paired = append(rep.Paired, p)
	}

	settings := support.Settings{WithdrawalDays: cfg.WithdrawalDays, DoubleChargeMinutes: cfg.DoubleChargeMinutes}
	agents := []struct {
		name             string
		verify, automate bool
	}{{"people only (today)", false, false}, {"agent without checks", false, true}, {"agent with evidence checks", true, true}}
	proposed := design(Proposed)
	for _, a := range agents {
		b := NewBilling(w)
		res := &support.Resolver{Reader: support.Rules{}, Billing: b, Directory: NewDirectory(w), Settings: settings,
			Verify: a.verify, Automate: a.automate}
		_, out, err := Run(ctx, w, cfg, proposed, store.NewMemory(), tickets, res, false)
		if err != nil {
			return rep, err
		}
		rep.Support = append(rep.Support, ScoreSupport(a.name, tickets, out, b, wc.Tickets.PersonMinutes))
	}

	v, err := ScoreVAT(ctx, w, cfg)
	if err != nil {
		return rep, err
	}
	rep.VAT = v
	rep.Gate = Decide(rep, cfg)
	rep.Took = time.Since(t0).Round(time.Millisecond).String()
	return rep, nil
}

func design(name string) Design {
	for _, d := range Designs {
		if d.Name == name {
			return d
		}
	}
	panic("unknown design " + name)
}

// Decide applies the gate in config/shop.json to the proposed design.
func Decide(rep Report, cfg ShopConfig) Gate {
	g := Gate{Design: Proposed, Pass: true}
	check := func(ok bool, msg string) {
		if !ok {
			g.Pass = false
			msg = "FAIL " + msg
		} else {
			msg = "ok " + msg
		}
		g.Checks = append(g.Checks, msg)
	}
	for _, a := range rep.Access {
		if a.Design != Proposed {
			continue
		}
		check(a.WrongDenialsPer1k <= cfg.Gate.MaxWrongDenials, f("wrong denials %.2f per 1,000 checks (max %.2f)", a.WrongDenialsPer1k, cfg.Gate.MaxWrongDenials))
		check(a.LeakedEuros <= cfg.Gate.MaxLeakedEuros, f("leaked access €%.0f (max €%.0f)", a.LeakedEuros, cfg.Gate.MaxLeakedEuros))
	}
	for _, s := range rep.Support {
		if s.Agent == "agent with evidence checks" {
			check(s.WrongMoney <= cfg.Gate.MaxWrongMoney, f("wrong money or access actions %d (max %d)", s.WrongMoney, cfg.Gate.MaxWrongMoney))
			check(s.DoubleRefunds == 0, f("double refunds %d (max 0)", s.DoubleRefunds))
		}
	}
	for _, v := range rep.VAT {
		if v.Method == "evidence + VIES" {
			per := 1000 * float64(v.Silent) / float64(v.Checkouts)
			check(per <= cfg.Gate.MaxVATErrorsPer1k, f("VAT errors not kept for review %.2f per 1,000 checkouts (max %.2f)", per, cfg.Gate.MaxVATErrorsPer1k))
		}
	}
	return g
}

// Paired compares two designs check by check on the same sessions: how many answers differ, and in whose favour.
type Paired struct {
	A          string  `json:"design"`
	B          string  `json:"against"`
	OnlyAWrong int     `json:"only_design_wrong"`
	OnlyBWrong int     `json:"only_proposed_wrong"`
	SignTestP  float64 `json:"sign_test_p"`
}

// SignTest is the two-sided exact sign test on discordant pairs (normal approximation above 200).
func SignTest(a, b int) float64 {
	n := a + b
	if n == 0 {
		return 1
	}
	k := a
	if b < a {
		k = b
	}
	if n > 200 {
		z := (math.Abs(float64(a-b)) - 1) / math.Sqrt(float64(n))
		return math.Min(1, math.Erfc(z/math.Sqrt2))
	}
	p := 0.0
	for i := 0; i <= k; i++ {
		p += math.Exp(lchoose(n, i) - float64(n)*math.Ln2)
	}
	return math.Min(1, 2*p)
}

func lchoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

// TopConfusions lists the most common wrong actions.
func TopConfusions(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || all[i].v == all[j].v && all[i].k < all[j].k })
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, f("%s ×%d", all[i].k, all[i].v))
	}
	return out
}
