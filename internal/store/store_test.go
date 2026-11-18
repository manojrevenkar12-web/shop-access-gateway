package store_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
)

// stores returns every implementation the contract is checked against: memory always, MySQL when
// SHOP_TEST_MYSQL_DSN is set (CI runs a mysql:8.0 service).
func stores(t *testing.T) map[string]store.Store {
	t.Helper()
	out := map[string]store.Store{"memory": store.NewMemory()}
	if dsn := os.Getenv("SHOP_TEST_MYSQL_DSN"); dsn != "" {
		m, err := store.OpenMySQL(context.Background(), dsn)
		if err != nil {
			t.Fatalf("mysql: %v", err)
		}
		if err := m.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close() })
		out["mysql"] = m
	}
	return out
}

var t0 = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func sub(id string, user int64, status shop.Status, end time.Time, version int64) *shop.Sub {
	return &shop.Sub{ID: id, Provider: shop.Chargebee, User: user, Plan: "monthly", Status: status, StartedAt: t0,
		PeriodStart: t0, PeriodEnd: end, Version: version}
}

func TestContract(t *testing.T) {
	ctx := context.Background()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			end := t0.Add(30 * 24 * time.Hour)
			c := store.Change{Provider: shop.Chargebee, EventID: "evt_1", Sub: sub("cb_1", 7, shop.Active, end, 1),
				Until: end, Ordered: true, Source: "webhook evt_1", At: t0}
			if err := st.Apply(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err := st.Apply(ctx, c); !errors.Is(err, store.ErrDuplicate) {
				t.Fatalf("second delivery of one event: got %v, want ErrDuplicate", err)
			}
			// An older snapshot arriving late is refused, and its event is still marked processed.
			old := store.Change{Provider: shop.Chargebee, EventID: "evt_0", Sub: sub("cb_1", 7, shop.Trialing, t0.Add(time.Hour), 0),
				Until: t0.Add(time.Hour), Ordered: true, Source: "webhook evt_0", At: t0.Add(time.Minute)}
			if err := st.Apply(ctx, old); !errors.Is(err, store.ErrStale) {
				t.Fatalf("older snapshot: got %v, want ErrStale", err)
			}
			if err := st.Apply(ctx, old); !errors.Is(err, store.ErrDuplicate) {
				t.Fatalf("a refused stale event must stay processed: got %v", err)
			}
			rec, ok, err := st.Sub(ctx, "cb_1")
			if err != nil || !ok || rec.Sub.Status != shop.Active || !rec.Until.Equal(end) || rec.Source != "webhook evt_1" {
				t.Fatalf("stored record %+v ok=%v err=%v", rec, ok, err)
			}
			// A newer snapshot replaces it; the history keeps both.
			canc := sub("cb_1", 7, shop.Canceled, end, 2)
			canc.EndedAt = t0.Add(48 * time.Hour)
			if err := st.Apply(ctx, store.Change{Provider: shop.Chargebee, EventID: "evt_2", Sub: canc, Until: canc.EndedAt,
				Ordered: true, Source: "webhook evt_2", At: t0.Add(48 * time.Hour)}); err != nil {
				t.Fatal(err)
			}
			h, err := st.History(ctx, "cb_1")
			if err != nil || len(h) != 2 || h[0].Sub.Status != shop.Active || h[1].Sub.Status != shop.Canceled {
				t.Fatalf("history %+v err=%v", h, err)
			}
			for _, tc := range []struct {
				at   time.Time
				want bool
			}{{t0.Add(-time.Minute), false}, {t0.Add(time.Hour), true}, {t0.Add(47 * time.Hour), true}, {t0.Add(49 * time.Hour), false}} {
				got, err := st.Access(ctx, 7, tc.at)
				if err != nil || got != tc.want {
					t.Errorf("access at %s: got %v want %v (err %v)", tc.at, got, tc.want, err)
				}
			}
			if subs, err := st.SubsOfUser(ctx, 7); err != nil || len(subs) != 1 {
				t.Fatalf("subs of user: %v %v", subs, err)
			}
			if subs, err := st.Subs(ctx, shop.Chargebee); err != nil || len(subs) != 1 {
				t.Fatalf("subs of provider: %v %v", subs, err)
			}

			// Campus licences.
			l := store.Licence{ID: "lic_1", Code: "charite-2026", Seats: 2, Starts: t0, Ends: t0.Add(90 * 24 * time.Hour), Customer: "charite"}
			if err := st.PutLicence(ctx, l); err != nil {
				t.Fatal(err)
			}
			if _, err := st.JoinCampus(ctx, "nope-2026", 1, t0.Add(time.Hour)); !errors.Is(err, store.ErrUnknownCode) {
				t.Fatalf("unknown code: %v", err)
			}
			if _, err := st.JoinCampus(ctx, "charite-2026", 1, t0.Add(-time.Hour)); !errors.Is(err, store.ErrNotActive) {
				t.Fatalf("before the licence starts: %v", err)
			}
			for _, u := range []int64{1, 1, 2} { // joining twice keeps one seat
				if _, err := st.JoinCampus(ctx, "charite-2026", u, t0.Add(time.Hour)); err != nil {
					t.Fatalf("join %d: %v", u, err)
				}
			}
			if _, err := st.JoinCampus(ctx, "charite-2026", 3, t0.Add(time.Hour)); !errors.Is(err, store.ErrFull) {
				t.Fatalf("third member of a two-seat licence: %v", err)
			}
			if n, err := st.SeatCount(ctx, "lic_1"); err != nil || n != 2 {
				t.Fatalf("seats %d %v", n, err)
			}
			if ok, _ := st.Access(ctx, 1, t0.Add(2*time.Hour)); !ok {
				t.Fatal("a seat grants access")
			}
			if ok, _ := st.Access(ctx, 1, t0.Add(30*time.Minute)); ok {
				t.Fatal("a seat grants nothing before the member joined")
			}
			if ls, err := st.SeatsOf(ctx, 2); err != nil || len(ls) != 1 || ls[0].Code != "charite-2026" {
				t.Fatalf("seats of 2: %v %v", ls, err)
			}
		})
	}
}

func TestSeatsNeverOversold(t *testing.T) {
	ctx := context.Background()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			l := store.Licence{ID: "lic_r", Code: "race-2026", Seats: 10, Starts: t0, Ends: t0.Add(time.Hour * 24), Customer: "r"}
			if err := st.PutLicence(ctx, l); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			errs := make(chan error, 50)
			for i := 0; i < 50; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if _, err := st.JoinCampus(ctx, "race-2026", int64(5000+i), t0.Add(time.Minute)); err != nil &&
						!errors.Is(err, store.ErrFull) {
						errs <- err
					}
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			if n, _ := st.SeatCount(ctx, "lic_r"); n != 10 {
				t.Fatalf("50 concurrent joins on a 10-seat licence took %d seats", n)
			}
		})
	}
}

func TestUnorderedWritesAcceptOlder(t *testing.T) {
	// Without ordering (the naive designs) a late older snapshot overwrites a newer one: the bug the designs show.
	ctx := context.Background()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			end := t0.Add(30 * 24 * time.Hour)
			_ = st.Apply(ctx, store.Change{Provider: shop.Chargebee, Sub: sub("cb_u", 8, shop.Canceled, end, 2), Until: t0, At: t0})
			if err := st.Apply(ctx, store.Change{Provider: shop.Chargebee, Sub: sub("cb_u", 8, shop.Active, end, 1), Until: end, At: t0}); err != nil {
				t.Fatal(err)
			}
			rec, _, _ := st.Sub(ctx, "cb_u")
			if rec.Sub.Status != shop.Active {
				t.Fatalf("unordered write should store what came last, got %s", rec.Sub.Status)
			}
		})
	}
}
