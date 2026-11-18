package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/lab"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

// bench replays a smaller dev world into MySQL and into memory, compares every answer, and measures the ledger.
func bench(ctx context.Context, root, dsn string, customers int) error {
	if dsn == "" {
		return fmt.Errorf("bench needs a MySQL DSN (-dsn or SHOP_MYSQL_DSN)")
	}
	cfg, err := lab.LoadShop(filepath.Join(root, "config", "shop.json"))
	if err != nil {
		return err
	}
	wc, err := world.Load(filepath.Join(root, "config", "world.json"))
	if err != nil {
		return err
	}
	wc.Customers = customers
	db, err := store.OpenMySQL(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Reset(ctx); err != nil {
		return err
	}
	var version string
	_ = db.DB().QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	rep, err := lab.Bench(ctx, version, wc, cfg, db)
	if err != nil {
		return err
	}
	fmt.Printf("%s: replay of %d deliveries and %d checks in %.1fs, same answers %v, same ledger %v; access p50 %.0fµs p99 %.0fµs, %.0f checks/s; ingest %.0f events/s with %d workers; %s\n",
		rep.Engine, rep.Deliveries, rep.Checks, rep.ReplaySeconds, rep.SameAnswers, rep.SameLedger,
		rep.AccessP50Micros, rep.AccessP99Micros, rep.ParallelChecksPerS, rep.IngestEventsPerS, rep.IngestWorkers, rep.SeatRace)
	return writeJSON(filepath.Join(root, "reports", "bench.json"), rep)
}
