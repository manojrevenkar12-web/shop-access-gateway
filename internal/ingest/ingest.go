// Package ingest turns what the payment providers say into the entitlement ledger.
//
// Webhooks arrive twice, late, out of order or never. The processor makes each one count once (the event id is
// recorded in the same transaction as its effect), applies a snapshot only if it is newer than the stored one
// (Chargebee's resource_version; for Stripe, which sends no version, by reading the object back from the API), lets
// a customer in as soon as they return from checkout (reading the session instead of waiting for the webhook), and
// repairs anything that was never delivered with a nightly reconciliation against the providers' APIs.
//
// Each of those is a switch in Design, so the lab can measure what each one is worth.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
)

// ProviderAPI reads objects from a provider's REST API as they are now.
type ProviderAPI interface {
	Get(ctx context.Context, p shop.Provider, id string) (shop.Sub, bool, error)
	List(ctx context.Context, p shop.Provider) ([]shop.Sub, error)
}

// Design is which safeguards the processor uses.
type Design struct {
	Name           string `json:"name"`
	Dedupe         bool   `json:"dedupe"`           // count each event id once
	Order          bool   `json:"order"`            // refuse snapshots older than the stored one
	Refetch        bool   `json:"refetch"`          // Stripe: read the object back instead of trusting the payload
	Accumulate     bool   `json:"accumulate"`       // naive: a paid invoice extends access by one period
	FulfilOnReturn bool   `json:"fulfil_on_return"` // read the checkout when the customer returns from paying
	Reconcile      bool   `json:"reconcile"`        // nightly: compare every object with the provider's API
	Buffer         bool   `json:"renewal_buffer"`   // keep a renewing subscription open until its renewal arrives
}

func (p *Processor) policy() shop.Policy {
	pol := p.Policy
	if !p.Design.Buffer {
		pol.RenewalBuffer = 0
	}
	return pol
}

// Processor applies provider data to the ledger.
type Processor struct {
	Store  store.Store
	API    ProviderAPI
	Policy shop.Policy
	Plans  map[string]shop.Plan
	Design Design
}

// Outcome of handling one delivery.
type Outcome string

const (
	Applied   Outcome = "applied"
	Duplicate Outcome = "duplicate"
	Stale     Outcome = "stale"
)

// Handle applies one webhook delivery at time now.
func (p *Processor) Handle(ctx context.Context, e shop.Event, now time.Time) (Outcome, error) {
	snap := e.Object
	ordered := p.Design.Order
	if p.Design.Refetch && e.Provider == shop.Stripe {
		cur, ok, err := p.API.Get(ctx, e.Provider, e.Object.ID)
		if err != nil {
			return "", fmt.Errorf("refetch %s: %w", e.Object.ID, err)
		}
		if ok {
			snap = cur
			snap.Version = now.UnixNano() // a later read is a newer state
		}
	} else if ordered && snap.Version == 0 {
		snap.Version = e.Created.UnixNano() // no version and no read: the event's own timestamp is the best guess
	}
	if p.Design.Accumulate && e.Type == shop.InvoicePaid {
		if old, ok, err := p.Store.Sub(ctx, snap.ID); err != nil {
			return "", err
		} else if ok { // the classic bug: "paid, so add a month" — a duplicate adds another
			plan := p.Plans[old.Sub.Plan]
			x := old.Sub
			x.Status, x.PeriodStart, x.PeriodEnd = shop.Active, old.Sub.PeriodEnd, old.Sub.PeriodEnd.Add(plan.Period)
			snap = x
		}
	}
	c := store.Change{Provider: e.Provider, Sub: &snap, Until: shop.AccessUntil(snap, p.policy()), Ordered: ordered,
		Source: "webhook " + e.ID, At: now}
	if p.Design.Dedupe {
		c.EventID = e.ID
	}
	switch err := p.Store.Apply(ctx, c); {
	case errors.Is(err, store.ErrDuplicate):
		return Duplicate, nil
	case errors.Is(err, store.ErrStale):
		return Stale, nil
	case err != nil:
		return "", err
	}
	return Applied, nil
}

// Return is the customer landing on the success page: with FulfilOnReturn, read the checkout and grant now.
func (p *Processor) Return(ctx context.Context, provider shop.Provider, subID string, now time.Time) (bool, error) {
	if !p.Design.FulfilOnReturn {
		return false, nil
	}
	return p.sync(ctx, provider, subID, now, "return")
}

// Sync reads one object from its provider and stores it if newer (support uses it too).
func (p *Processor) Sync(ctx context.Context, provider shop.Provider, subID string, now time.Time, why string) (bool, error) {
	return p.sync(ctx, provider, subID, now, why)
}

func (p *Processor) sync(ctx context.Context, provider shop.Provider, id string, now time.Time, why string) (bool, error) {
	cur, ok, err := p.API.Get(ctx, provider, id)
	if err != nil || !ok {
		return false, err
	}
	return p.store(ctx, cur, now, why)
}

func (p *Processor) store(ctx context.Context, cur shop.Sub, now time.Time, why string) (bool, error) {
	if cur.Provider == shop.Stripe {
		cur.Version = now.UnixNano()
	}
	if old, ok, err := p.Store.Sub(ctx, cur.ID); err != nil {
		return false, err
	} else if ok && same(old.Sub, cur) {
		return false, nil
	}
	err := p.Store.Apply(ctx, store.Change{Provider: cur.Provider, Sub: &cur, Until: shop.AccessUntil(cur, p.policy()),
		Ordered: true, Source: why, At: now})
	if errors.Is(err, store.ErrStale) {
		return false, nil
	}
	return err == nil, err
}

func same(a, b shop.Sub) bool {
	return a.Status == b.Status && a.PeriodStart.Equal(b.PeriodStart) && a.PeriodEnd.Equal(b.PeriodEnd) &&
		a.EndedAt.Equal(b.EndedAt) && a.CancelAtPeriodEnd == b.CancelAtPeriodEnd && a.Plan == b.Plan
}

// Reconciliation is what a nightly run repaired.
type Reconciliation struct {
	Checked  int `json:"checked"`
	Repaired int `json:"repaired"`
	Missing  int `json:"missing"` // objects the ledger had never heard of
}

// Reconcile compares every object at both providers with the ledger and stores the providers' state where the
// ledger differs. It never writes an older state: Chargebee versions and Stripe read times order it.
func (p *Processor) Reconcile(ctx context.Context, now time.Time) (Reconciliation, error) {
	var rec Reconciliation
	if !p.Design.Reconcile {
		return rec, nil
	}
	for _, prov := range []shop.Provider{shop.Stripe, shop.Chargebee} {
		subs, err := p.API.List(ctx, prov)
		if err != nil {
			return rec, err
		}
		for _, cur := range subs {
			rec.Checked++
			_, known, err := p.Store.Sub(ctx, cur.ID)
			if err != nil {
				return rec, err
			}
			changed, err := p.store(ctx, cur, now, "reconcile")
			if err != nil {
				return rec, err
			}
			if changed {
				rec.Repaired++
				if !known {
					rec.Missing++
				}
			}
		}
	}
	return rec, nil
}
