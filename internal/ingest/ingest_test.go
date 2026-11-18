package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/ingest"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
)

var t0 = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

const month = 30 * 24 * time.Hour

// provider is a fake provider API holding the current state of each object.
type provider struct {
	now   map[string]shop.Sub
	calls int
}

func (p *provider) Get(_ context.Context, _ shop.Provider, id string) (shop.Sub, bool, error) {
	p.calls++
	s, ok := p.now[id]
	return s, ok, nil
}

func (p *provider) List(_ context.Context, prov shop.Provider) ([]shop.Sub, error) {
	var out []shop.Sub
	for _, s := range p.now {
		if s.Provider == prov {
			out = append(out, s)
		}
	}
	return out, nil
}

func snap(status shop.Status, start time.Time, version int64) shop.Sub {
	return shop.Sub{ID: "cb_1", Provider: shop.Chargebee, User: 7, Plan: "monthly", Status: status, StartedAt: t0,
		PeriodStart: start, PeriodEnd: start.Add(month), Version: version}
}

func event(id, typ string, s shop.Sub, at time.Time) shop.Event {
	return shop.Event{ID: id, Provider: s.Provider, Type: typ, Created: at, Object: s}
}

func processor(d ingest.Design, api *provider) (*ingest.Processor, store.Store) {
	st := store.NewMemory()
	return &ingest.Processor{Store: st, API: api, Design: d, Policy: shop.Policy{Grace: 7 * 24 * time.Hour,
		RenewalBuffer: 30 * time.Hour, Recurring: map[string]bool{"monthly": true}},
		Plans: map[string]shop.Plan{"monthly": {ID: "monthly", Period: month, PriceCents: 3900, Recurring: true}}}, st
}

func until(t *testing.T, st store.Store) time.Time {
	t.Helper()
	r, ok, err := st.Sub(context.Background(), "cb_1")
	if err != nil || !ok {
		t.Fatalf("no record: %v", err)
	}
	return r.Until
}

func TestDuplicateRenewalAddsAFreeMonthOnlyInTheNaiveDesign(t *testing.T) {
	ctx := context.Background()
	buy := event("evt_0", shop.CheckoutCompleted, snap(shop.Active, t0, 1), t0)
	renew := event("evt_1", shop.InvoicePaid, snap(shop.Active, t0.Add(month), 2), t0.Add(month))
	for _, tc := range []struct {
		d    ingest.Design
		want time.Time
	}{
		{ingest.Design{Name: "naive", Accumulate: true}, t0.Add(3 * month)},
		{ingest.Design{Name: "dedupe", Dedupe: true}, t0.Add(2 * month)},
	} {
		p, st := processor(tc.d, &provider{})
		for _, e := range []shop.Event{buy, renew, renew} {
			if _, err := p.Handle(ctx, e, e.Created.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		if got := until(t, st); !got.Equal(tc.want) {
			t.Errorf("%s: access until %s, want %s", tc.d.Name, got, tc.want)
		}
	}
}

func TestLateOlderEventCannotReopenACancelledSubscription(t *testing.T) {
	ctx := context.Background()
	active := event("evt_1", shop.InvoicePaid, snap(shop.Active, t0, 1), t0)
	c := snap(shop.Canceled, t0, 2)
	c.EndedAt = t0.Add(time.Hour)
	cancel := event("evt_2", shop.ChargeRefunded, c, t0.Add(time.Hour))
	for _, tc := range []struct {
		d       ingest.Design
		granted bool
	}{
		{ingest.Design{Name: "dedupe", Dedupe: true}, true}, // arrival order wins: the refund is undone
		{ingest.Design{Name: "ordered", Dedupe: true, Order: true}, false},
	} {
		p, st := processor(tc.d, &provider{})
		_, _ = p.Handle(ctx, cancel, t0.Add(2*time.Hour))
		out, _ := p.Handle(ctx, active, t0.Add(3*time.Hour)) // delivered late
		if tc.d.Order && out != ingest.Stale {
			t.Errorf("ordered: late older event outcome %s, want stale", out)
		}
		got, _ := st.Access(ctx, 7, t0.Add(4*time.Hour))
		if got != tc.granted {
			t.Errorf("%s: access after the refund %v, want %v", tc.d.Name, got, tc.granted)
		}
	}
}

func TestReturnGrantsBeforeTheWebhookArrives(t *testing.T) {
	ctx := context.Background()
	s := snap(shop.Active, t0, 1)
	api := &provider{now: map[string]shop.Sub{"cb_1": s}}
	p, st := processor(ingest.Design{Dedupe: true, Order: true, FulfilOnReturn: true}, api)
	if ok, err := p.Return(ctx, shop.Chargebee, "cb_1", t0.Add(5*time.Second)); err != nil || !ok {
		t.Fatalf("return: %v %v", ok, err)
	}
	if got, _ := st.Access(ctx, 7, t0.Add(20*time.Second)); !got {
		t.Fatal("customer back from checkout should be in before the webhook")
	}
	// The webhook then arrives: same version, nothing changes.
	if out, _ := p.Handle(ctx, event("evt_0", shop.CheckoutCompleted, s, t0), t0.Add(time.Minute)); out != ingest.Stale {
		t.Fatalf("webhook after return: %s, want stale", out)
	}
}

func TestReconcileRepairsALostEvent(t *testing.T) {
	ctx := context.Background()
	first := snap(shop.Active, t0, 1)
	renewed := snap(shop.Active, t0.Add(month), 2) // its invoice.paid webhook was lost
	api := &provider{now: map[string]shop.Sub{"cb_1": renewed}}
	p, st := processor(ingest.Design{Dedupe: true, Order: true, Reconcile: true}, api)
	_, _ = p.Handle(ctx, event("evt_0", shop.CheckoutCompleted, first, t0), t0)
	rec, err := p.Reconcile(ctx, t0.Add(month+3*time.Hour))
	if err != nil || rec.Repaired != 1 || rec.Checked != 1 {
		t.Fatalf("reconcile %+v %v", rec, err)
	}
	if got := until(t, st); !got.Equal(t0.Add(2 * month)) {
		t.Fatalf("until %s after reconcile", got)
	}
	if rec, _ := p.Reconcile(ctx, t0.Add(month+4*time.Hour)); rec.Repaired != 0 {
		t.Fatalf("a second reconcile with nothing new repaired %d", rec.Repaired)
	}
}

func TestRenewalBufferBridgesALateRenewalButNotACancellation(t *testing.T) {
	ctx := context.Background()
	s := snap(shop.Active, t0, 1)
	p, st := processor(ingest.Design{Dedupe: true, Order: true, Buffer: true}, &provider{})
	_, _ = p.Handle(ctx, event("evt_0", shop.CheckoutCompleted, s, t0), t0)
	if got, _ := st.Access(ctx, 7, t0.Add(month+2*time.Hour)); !got {
		t.Fatal("renewal webhook two hours late: the buffer keeps the customer in")
	}
	c := s
	c.CancelAtPeriodEnd, c.Version = true, 2
	_, _ = p.Handle(ctx, event("evt_1", shop.SubUpdated, c, t0.Add(time.Hour)), t0.Add(time.Hour))
	if got, _ := st.Access(ctx, 7, t0.Add(month+time.Minute)); got {
		t.Fatal("a subscription set to end gets no buffer")
	}
}

func TestStripeRefetchReadsCurrentState(t *testing.T) {
	ctx := context.Background()
	cur := shop.Sub{ID: "sub_1", Provider: shop.Stripe, User: 9, Plan: "monthly", Status: shop.Canceled, StartedAt: t0,
		PeriodStart: t0, PeriodEnd: t0.Add(month), EndedAt: t0.Add(time.Hour)}
	api := &provider{now: map[string]shop.Sub{"sub_1": cur}}
	p, st := processor(ingest.Design{Dedupe: true, Order: true, Refetch: true}, api)
	stale := cur
	stale.Status, stale.EndedAt = shop.Active, time.Time{}
	if _, err := p.Handle(ctx, shop.Event{ID: "evt_s", Provider: shop.Stripe, Type: shop.InvoicePaid, Created: t0, Object: stale}, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if api.calls != 1 {
		t.Fatalf("refetch calls %d", api.calls)
	}
	if got, _ := st.Access(ctx, 9, t0.Add(3*time.Hour)); got {
		t.Fatal("refetched state is cancelled: no access")
	}
}
