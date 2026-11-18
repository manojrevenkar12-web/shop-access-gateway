package lab

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

func small(t *testing.T, customers int) (world.Config, ShopConfig) {
	t.Helper()
	wc, err := world.Load(filepath.Join(Root(), "config", "world.json"))
	if err != nil {
		t.Fatal(err)
	}
	wc.Customers = customers
	cfg, err := LoadShop(filepath.Join(Root(), "config", "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	return wc, cfg
}

func TestStudyOrdersTheDesigns(t *testing.T) {
	wc, cfg := small(t, 1500)
	rep, err := Study(context.Background(), "test", wc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]AccessResult{}
	for _, a := range rep.Access {
		by[a.Design] = a
	}
	if by["gateway"].WrongDenials >= by["naive"].WrongDenials || by["gateway"].WrongDenials > by["plus_fulfil"].WrongDenials {
		t.Errorf("wrong denials: naive %d, plus_fulfil %d, gateway %d", by["naive"].WrongDenials, by["plus_fulfil"].WrongDenials, by["gateway"].WrongDenials)
	}
	if by["naive"].LeakedEuros <= by["gateway"].LeakedEuros {
		t.Errorf("duplicate renewals should leak access in the naive design: naive €%.0f gateway €%.0f", by["naive"].LeakedEuros, by["gateway"].LeakedEuros)
	}
	if by["gateway"].FirstUseDenied > by["dedupe_order"].FirstUseDenied {
		t.Error("fulfilling on return should cut first-use denials")
	}
	for _, s := range rep.Support {
		switch s.Agent {
		case "agent with evidence checks":
			if s.WrongMoney != 0 || s.DoubleRefunds != 0 {
				t.Errorf("verified agent: %d wrong money actions, %d double refunds", s.WrongMoney, s.DoubleRefunds)
			}
		case "agent without checks":
			if s.WrongMoney == 0 {
				t.Error("the agent without checks should make wrong money or access actions in this world")
			}
		}
	}
	if rep.VAT[1].Wrong >= rep.VAT[0].Wrong {
		t.Errorf("VAT: evidence %d errors, billing country %d", rep.VAT[1].Wrong, rep.VAT[0].Wrong)
	}
}

func TestSameLuckSameNumbers(t *testing.T) {
	wc, cfg := small(t, 600)
	a, err := Study(context.Background(), "a", wc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Study(context.Background(), "a", wc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Access {
		a.Access[i].EventsPerSecond, b.Access[i].EventsPerSecond = 0, 0
	}
	a.Took, b.Took = "", ""
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two runs of one world gave different numbers")
	}
	// Every design sees the same sessions: the same number of checks.
	for _, r := range a.Access[1:] {
		if r.Checks != a.Access[0].Checks {
			t.Fatalf("%s made %d checks, naive %d", r.Design, r.Checks, a.Access[0].Checks)
		}
	}
}

func TestMySQLGivesTheSameAnswersAsMemory(t *testing.T) {
	dsn := os.Getenv("SHOP_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("SHOP_TEST_MYSQL_DSN not set")
	}
	ctx := context.Background()
	db, err := store.OpenMySQL(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	wc, cfg := small(t, 250)
	w := world.New(wc)
	mem, _, err := Run(ctx, w, cfg, design(Proposed), store.NewMemory(), nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	sql, _, err := Run(ctx, w, cfg, design(Proposed), db, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mem.Answers, sql.Answers) || mem.WrongDenials != sql.WrongDenials || mem.LeakedEuros != sql.LeakedEuros {
		t.Fatalf("memory and MySQL disagree: memory %d wrong denials, MySQL %d", mem.WrongDenials, sql.WrongDenials)
	}
}

func TestSignTest(t *testing.T) {
	if p := SignTest(0, 0); p != 1 {
		t.Fatalf("no discordant pairs: %v", p)
	}
	if p := SignTest(10, 0); p > 0.002 || p < 0.001 {
		t.Fatalf("10 to 0: %v, want 2/1024", p)
	}
	if p := SignTest(5, 5); p != 1 {
		t.Fatalf("5 to 5: %v", p)
	}
}
