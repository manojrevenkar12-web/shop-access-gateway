// Command shop runs the gateway (serve), the design study (study), the MySQL benchmark (bench) and regenerates the
// README dashboard (readme).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/lab"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: shop serve|study|bench|readme [flags]")
		os.Exit(2)
	}
	root := lab.Root()
	ctx := context.Background()
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "study":
		fs := flag.NewFlagSet("study", flag.ExitOnError)
		name := fs.String("world", "dev", "dev or heldout")
		_ = fs.Parse(args)
		if err := study(ctx, root, *name); err != nil {
			log.Fatal(err)
		}
	case "bench":
		fs := flag.NewFlagSet("bench", flag.ExitOnError)
		dsn := fs.String("dsn", os.Getenv("SHOP_MYSQL_DSN"), "MySQL DSN")
		customers := fs.Int("customers", 2500, "customers in the benchmark world")
		_ = fs.Parse(args)
		if err := bench(ctx, root, *dsn, *customers); err != nil {
			log.Fatal(err)
		}
	case "readme":
		if err := readme(root); err != nil {
			log.Fatal(err)
		}
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		addr := fs.String("addr", ":8080", "listen address")
		dsn := fs.String("dsn", os.Getenv("SHOP_MYSQL_DSN"), "MySQL DSN (empty: in-memory store)")
		_ = fs.Parse(args)
		if err := serve(ctx, root, *addr, *dsn); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown command %q", cmd)
	}
}

func study(ctx context.Context, root, name string) error {
	cfgFile := map[string]string{"dev": "world.json", "heldout": "heldout.json"}[name]
	if cfgFile == "" {
		return fmt.Errorf("world must be dev or heldout")
	}
	lock := filepath.Join(root, "reports", "heldout.lock")
	if name == "heldout" {
		if _, err := os.Stat(lock); err == nil {
			return fmt.Errorf("the held-out world has been run once already (%s); see docs/heldout.md", lock)
		}
	}
	wc, err := world.Load(filepath.Join(root, "config", cfgFile))
	if err != nil {
		return err
	}
	cfg, err := lab.LoadShop(filepath.Join(root, "config", "shop.json"))
	if err != nil {
		return err
	}
	rep, err := lab.Study(ctx, name, wc, cfg)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(root, "reports", name+".json"), rep); err != nil {
		return err
	}
	if name == "heldout" {
		if err := os.WriteFile(lock, []byte("run once; do not delete\n"), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("%s world: %d customers, %d deliveries, gate pass=%v (%s)\n", name, rep.Customers, rep.Deliveries,
		rep.Gate.Pass, rep.Took)
	for _, c := range rep.Gate.Checks {
		fmt.Println("  " + c)
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
