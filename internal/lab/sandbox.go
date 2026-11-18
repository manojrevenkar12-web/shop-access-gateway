package lab

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/ingest"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/support"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/vat"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

// Sandbox is the gateway running against the simulated providers, for the console and local development: the
// world's history is replayed into the store, then the clock keeps running from the end of the history.
type Sandbox struct {
	World     *world.World
	Processor *ingest.Processor
	Resolver  *support.Resolver
	VIES      vat.Checker
	clock     atomic.Int64
	boot      time.Time
}

// NewSandbox replays the world into st with the proposed design.
func NewSandbox(ctx context.Context, wc world.Config, cfg ShopConfig, st store.Store) (*Sandbox, error) {
	w := world.New(wc)
	if _, _, err := Run(ctx, w, cfg, design(Proposed), st, nil, nil, false); err != nil {
		return nil, err
	}
	sb := &Sandbox{World: w, VIES: VIES(w), boot: time.Now()}
	sb.clock.Store(w.End.UnixNano())
	var calls atomic.Int64
	sb.Processor = &ingest.Processor{Store: st, API: api{w, sb.Now, &calls}, Policy: cfg.Policy(), Plans: cfg.PlanMap(),
		Design: design(Proposed).Design}
	b := NewBilling(w)
	b.clock = sb.Now
	sb.Resolver = &support.Resolver{Reader: support.Rules{}, Billing: b, Ledger: st, Processor: sb.Processor,
		Directory: NewDirectory(w), Settings: support.Settings{WithdrawalDays: cfg.WithdrawalDays,
			DoubleChargeMinutes: cfg.DoubleChargeMinutes}, Verify: true, Automate: true}
	return sb, nil
}

// Now is the sandbox clock: the end of the replayed history plus the time the server has been running.
func (s *Sandbox) Now() time.Time { return time.Unix(0, s.clock.Load()).Add(time.Since(s.boot)).UTC() }

// Advance moves the sandbox clock.
func (s *Sandbox) Advance(d time.Duration) { s.clock.Add(int64(d)) }
