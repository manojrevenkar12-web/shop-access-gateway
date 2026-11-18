package world_test

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

func load(t *testing.T, name string) world.Config {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	wc, err := world.Load(filepath.Join(filepath.Dir(file), "..", "..", "config", name))
	if err != nil {
		t.Fatal(err)
	}
	return wc
}

func TestKeyedLuck(t *testing.T) {
	// A customer's history depends on the seed and the customer, not on how many others there are.
	a := load(t, "world.json")
	a.Customers = 200
	b := a
	b.Customers = 400
	wa, wb := world.New(a), world.New(b)
	for i := 0; i < 200; i++ {
		x, y := wa.Subs[i], wb.Subs[i]
		if x.ID != y.ID || x.Plan != y.Plan || len(x.Events) != len(y.Events) || !x.Events[0].Created.Equal(y.Events[0].Created) {
			t.Fatalf("customer %d differs between a 200- and a 400-customer world", i)
		}
	}
}

func TestHeldoutDiffersButKeepsTheBase(t *testing.T) {
	dev, held := load(t, "world.json"), load(t, "heldout.json")
	if held.Seed == dev.Seed || held.Providers.ChargebeeShare == dev.Providers.ChargebeeShare {
		t.Fatal("the held-out world must differ from dev")
	}
	if held.Days != dev.Days || len(held.Plans) == 0 {
		t.Fatal("the held-out world inherits what it does not override")
	}
}

func TestTruthFollowsTheProviderState(t *testing.T) {
	wc := load(t, "world.json")
	wc.Customers = 300
	w := world.New(wc)
	for _, s := range w.Subs {
		first := s.Events[0]
		if first.Object.Status != shop.Active {
			continue
		}
		if !w.Entitled(s.User, first.Created.Add(time.Minute)) {
			t.Fatalf("%s: a paid customer is entitled a minute after paying", s.ID)
		}
		x, ok := w.Get(s.Provider, s.ID, first.Created.Add(-time.Second))
		if ok {
			t.Fatalf("%s: the provider knows the object before it existed: %+v", s.ID, x)
		}
		return
	}
	t.Fatal("no active purchase in the world")
}

func TestDeliveryIsImperfect(t *testing.T) {
	wc := load(t, "world.json")
	wc.Customers = 2000
	w := world.New(wc)
	dups, events := 0, 0
	for _, a := range w.Arrivals {
		if a.Dup {
			dups++
		}
	}
	for _, s := range w.Subs {
		events += len(s.Events)
	}
	if dups == 0 {
		t.Fatal("the world should deliver some events twice")
	}
	if len(w.Arrivals)-dups >= events {
		t.Fatal("the world should lose some events")
	}
}
