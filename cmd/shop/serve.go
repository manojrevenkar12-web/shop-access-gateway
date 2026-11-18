package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/api"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/lab"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

// serve runs the gateway in sandbox mode: the simulated providers stand in for Stripe and Chargebee, their history
// is replayed into the ledger first, and the console is served from web/dist when it has been built.
func serve(ctx context.Context, root, addr, dsn string) error {
	cfg, err := lab.LoadShop(filepath.Join(root, "config", "shop.json"))
	if err != nil {
		return err
	}
	wc, err := world.Load(filepath.Join(root, "config", "world.json"))
	if err != nil {
		return err
	}
	var st store.Store = store.NewMemory()
	if dsn != "" {
		m, err := store.OpenMySQL(ctx, dsn)
		if err != nil {
			return err
		}
		if err := m.Reset(ctx); err != nil {
			return err
		}
		st = m
	}
	defer st.Close()
	t0 := time.Now()
	sb, err := lab.NewSandbox(ctx, wc, cfg, st)
	if err != nil {
		return err
	}
	log.Printf("replayed %d webhook deliveries in %s; sandbox clock %s", len(sb.World.Arrivals),
		time.Since(t0).Round(time.Millisecond), sb.Now().Format(time.RFC3339))
	secret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if secret == "" {
		secret = "whsec_sandbox"
	}
	srv := &api.Server{Store: st, Processor: sb.Processor, Resolver: sb.Resolver, VAT: cfg.VAT, VIES: sb.VIES,
		Plans: cfg.PlanMap(), Now: sb.Now,
		Secrets: api.Secrets{StripeSigning: secret, StripeTolerance: 5 * time.Minute,
			ChargebeeUser: env("CHARGEBEE_WEBHOOK_USER", "chargebee"), ChargebeePassword: env("CHARGEBEE_WEBHOOK_PASSWORD", "sandbox")},
		Report: func() ([]byte, error) { return os.ReadFile(filepath.Join(root, "reports", "dev.json")) }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sandbox/examples", func(w http.ResponseWriter, r *http.Request) {
		ex, err := examples(r.Context(), sb, st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ex)
	})
	mux.Handle("/v1/", srv.Handler())
	mux.Handle("/webhooks/", srv.Handler())
	mux.Handle("/graphql", srv.Handler())
	mux.Handle("/healthz", srv.Handler())
	dist := filepath.Join(root, "web", "dist")
	if _, err := os.Stat(dist); err == nil {
		mux.Handle("/", http.FileServer(http.Dir(dist)))
	}
	hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Printf("listening on %s\n", addr)
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Example is a customer worth looking up in the sandbox.
type Example struct {
	User  int64  `json:"user"`
	Label string `json:"label"`
}

// examples picks one customer for each situation support meets, from the replayed ledger.
func examples(ctx context.Context, sb *lab.Sandbox, st store.Store) ([]Example, error) {
	now := sb.Now()
	want := []struct {
		label string
		match func(rec store.Record, granted bool) bool
	}{
		{"paying, renews monthly", func(r store.Record, g bool) bool { return g && r.Sub.Status == shop.Active && r.Sub.Plan == "monthly" }},
		{"renewal failed, in grace", func(r store.Record, g bool) bool { return g && r.Sub.Status == shop.PastDue }},
		{"cancelled, paid to period end", func(r store.Record, g bool) bool { return g && r.Sub.CancelAtPeriodEnd }},
		{"refunded within 14 days", func(r store.Record, g bool) bool {
			return !g && r.Sub.Status == shop.Canceled && !r.Sub.EndedAt.IsZero() && r.Sub.EndedAt.Before(r.Sub.PeriodEnd)
		}},
		{"exam pass ran out", func(r store.Record, g bool) bool {
			return !g && r.Sub.Status == shop.Expired && r.Sub.Plan == "exam_pass"
		}},
	}
	var out []Example
	for _, w := range want {
		for _, c := range sb.World.Customers {
			if c.Campus != "" {
				continue
			}
			recs, err := st.SubsOfUser(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			g, err := st.Access(ctx, c.ID, now)
			if err != nil {
				return nil, err
			}
			if len(recs) == 1 && w.match(recs[0], g) {
				out = append(out, Example{c.ID, w.label})
				break
			}
		}
	}
	for _, c := range sb.World.Customers {
		if c.Campus == "" {
			continue
		}
		if seats, err := st.SeatsOf(ctx, c.ID); err == nil && len(seats) > 0 {
			out = append(out, Example{c.ID, "campus seat, " + seats[0].Customer})
			break
		}
	}
	return out, nil
}
