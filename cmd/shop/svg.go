package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/lab"
)

// dashboard draws the headline numbers and the design ladder as one SVG (GitHub renders it in the README).
func dashboard(dev lab.Report, haveHeld bool, held lab.Report) string {
	const (
		ink   = "#1d2a35"
		muted = "#5f6b76"
		rule  = "#d9dee3"
		good  = "#0f7b6c"
		bad   = "#b4532a"
		paper = "#ffffff"
		W     = 960
	)
	acc := func(r lab.Report, d string) lab.AccessResult {
		for _, a := range r.Access {
			if a.Design == d {
				return a
			}
		}
		return lab.AccessResult{}
	}
	sup := func(r lab.Report, a string) lab.SupportResult {
		for _, s := range r.Support {
			if s.Agent == a {
				return s
			}
		}
		return lab.SupportResult{}
	}
	nv, gw := acc(dev, "naive"), acc(dev, lab.Proposed)
	today, agent := sup(dev, "people only (today)"), sup(dev, "agent with evidence checks")
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	rows := len(dev.Access)
	H := 210 + rows*34 + 64
	w(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif">`, W, H, W, H)
	w(`<rect width="100%%" height="100%%" fill="%s"/>`, paper)
	w(`<text x="32" y="40" font-size="20" font-weight="600" fill="%s">Who gets in, and who pays: dev world, seed %d</text>`, ink, dev.Seed)
	w(`<text x="32" y="62" font-size="13" fill="%s">%s webhook deliveries, %s page checks, %s support tickets, the same for every design</text>`,
		muted, n(dev.Deliveries), n(gw.Checks), n(dev.Tickets))
	tiles := []struct{ label, before, after, was string }{
		{"customers wrongly locked out", n(nv.UsersDenied), n(gw.UsersDenied), "naive"},
		{"access given away", "€" + n(int(nv.LeakedEuros+.5)), "€" + n(int(gw.LeakedEuros+.5)), "naive"},
		{"tickets closed without a person", n(today.Automated), n(agent.AutomatedRight), "today"},
		{"support person-hours", fmt.Sprintf("%.0f", today.PersonHours), fmt.Sprintf("%.0f", agent.PersonHours), "today"},
	}
	tw := (W - 64 - 3*16) / 4
	for i, t := range tiles {
		x := 32 + i*(tw+16)
		w(`<rect x="%d" y="84" width="%d" height="92" rx="6" fill="none" stroke="%s"/>`, x, tw, rule)
		w(`<text x="%d" y="128" font-size="28" font-weight="700" fill="%s">%s</text>`, x+16, good, t.after)
		w(`<text x="%d" y="128" font-size="14" fill="%s" text-anchor="end"><tspan text-decoration="line-through">%s</tspan> %s</text>`, x+tw-14, bad, t.before, t.was)
		w(`<text x="%d" y="156" font-size="13" fill="%s">%s</text>`, x+16, muted, t.label)
	}
	top := 214
	w(`<text x="32" y="%d" font-size="14" font-weight="600" fill="%s">Wrong denials per 1,000 page checks (log scale), one row per design step</text>`, top, ink)
	maxv := 0.0
	for _, a := range dev.Access {
		maxv = math.Max(maxv, a.WrongDenialsPer1k)
	}
	x0, x1 := 250.0, float64(W-120)
	scale := func(v float64) float64 { // log from 0.1 to max
		lo, hi := math.Log10(0.1), math.Log10(maxv*1.2)
		v = math.Max(v, 0.1)
		return x0 + (math.Log10(v)-lo)/(hi-lo)*(x1-x0)
	}
	for i, a := range dev.Access {
		y := top + 20 + i*34
		col := muted
		if a.Design == lab.Proposed {
			col = good
		} else if a.Design == "naive" {
			col = bad
		}
		weight := "400"
		if a.Design == lab.Proposed {
			weight = "700"
		}
		w(`<text x="%d" y="%d" font-size="13" font-weight="%s" fill="%s">%s</text>`, 32, y+16, weight, ink, a.Design)
		w(`<rect x="%.0f" y="%d" width="%.1f" height="20" rx="3" fill="%s"/>`, x0, y+2, scale(a.WrongDenialsPer1k)-x0, col)
		w(`<text x="%.1f" y="%d" font-size="13" fill="%s">%.2f</text>`, scale(a.WrongDenialsPer1k)+8, y+17, ink, a.WrongDenialsPer1k)
	}
	foot := top + 20 + rows*34 + 26
	note := "Simulated providers and customers; see Limits in the README."
	if haveHeld {
		hg := acc(held, lab.Proposed)
		note = fmt.Sprintf("Held-out world (seed %d, run once): gateway %.2f wrong denials per 1,000, %s customers locked out, €%s given away. %s",
			held.Seed, hg.WrongDenialsPer1k, n(hg.UsersDenied), n(int(hg.LeakedEuros+.5)), note)
	}
	w(`<line x1="32" x2="%d" y1="%d" y2="%d" stroke="%s"/>`, W-32, foot-18, foot-18, rule)
	w(`<text x="32" y="%d" font-size="12" fill="%s">%s</text>`, foot, muted, note)
	w(`</svg>`)
	return b.String()
}
