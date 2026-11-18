package lab

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/ingest"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/world"
)

// BenchReport is what the MySQL ledger did on one replay.
type BenchReport struct {
	Engine             string  `json:"engine"`
	Customers          int     `json:"customers"`
	Deliveries         int     `json:"deliveries"`
	Checks             int     `json:"access_checks_in_replay"`
	ReplaySeconds      float64 `json:"replay_seconds"` // the whole history: deliveries, page checks, returns, nightly reconciles
	SameAnswers        bool    `json:"same_answers_as_memory"`
	DifferentAnswers   int     `json:"different_answers"`
	SameLedger         bool    `json:"same_ledger_as_memory"`
	AccessP50Micros    float64 `json:"access_p50_us"`
	AccessP99Micros    float64 `json:"access_p99_us"`
	ParallelChecksPerS float64 `json:"parallel_checks_per_second"`
	Workers            int     `json:"workers"`
	IngestEventsPerS   float64 `json:"ingest_events_per_second"`
	IngestWorkers      int     `json:"ingest_workers"`
	SeatRace           string  `json:"seat_race"`
	SeatsTaken         int     `json:"seats_taken"`
	SeatsOnLicence     int     `json:"seats_on_licence"`
}

// Bench replays a world into a real database and into memory and compares every answer, then measures the access
// check and races joins for a small licence.
func Bench(ctx context.Context, engine string, wc world.Config, cfg ShopConfig, db store.Store) (BenchReport, error) {
	w := world.New(wc)
	rep := BenchReport{Engine: engine, Customers: len(w.Customers), Deliveries: len(w.Arrivals)}
	d := design(Proposed)
	mem := store.NewMemory()
	a, _, err := Run(ctx, w, cfg, d, mem, nil, nil, true)
	if err != nil {
		return rep, err
	}
	t0 := time.Now()
	b, _, err := Run(ctx, w, cfg, d, db, nil, nil, true)
	if err != nil {
		return rep, err
	}
	rep.ReplaySeconds = time.Since(t0).Seconds()
	rep.Checks = len(a.Answers)
	for i := range a.Answers {
		if i >= len(b.Answers) || a.Answers[i] != b.Answers[i] {
			rep.DifferentAnswers++
		}
	}
	rep.SameAnswers = rep.DifferentAnswers == 0 && len(a.Answers) == len(b.Answers)
	rep.SameLedger = true
	for _, s := range w.Subs {
		x, okx, err := mem.Sub(ctx, s.ID)
		if err != nil {
			return rep, err
		}
		y, oky, err := db.Sub(ctx, s.ID)
		if err != nil {
			return rep, err
		}
		if okx != oky || !x.Until.Equal(y.Until) || x.Sub.Status != y.Sub.Status || x.Sub.Version != y.Sub.Version {
			rep.SameLedger = false
			break
		}
	}

	// Latency of the check every page makes.
	users := make([]int64, 0, len(w.Customers))
	for _, c := range w.Customers {
		users = append(users, c.ID)
	}
	at := w.End.Add(-time.Hour)
	var lat []float64
	for i := 0; i < 4000; i++ {
		u := users[(i*7919)%len(users)]
		t := time.Now()
		if _, err := db.Access(ctx, u, at); err != nil {
			return rep, err
		}
		lat = append(lat, float64(time.Since(t).Microseconds()))
	}
	sort.Float64s(lat)
	rep.AccessP50Micros, rep.AccessP99Micros = lat[len(lat)/2], lat[len(lat)*99/100]

	rep.Workers = 4 * runtime.GOMAXPROCS(0)
	var n atomic.Int64
	var wg sync.WaitGroup
	deadline := time.Now().Add(2 * time.Second)
	errc := make(chan error, rep.Workers)
	for g := 0; g < rep.Workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; time.Now().Before(deadline); i += rep.Workers {
				if _, err := db.Access(ctx, users[(i*104729)%len(users)], at); err != nil {
					errc <- err
					return
				}
				n.Add(1)
			}
		}(g)
	}
	wg.Wait()
	close(errc)
	if err := <-errc; err != nil {
		return rep, err
	}
	rep.ParallelChecksPerS = float64(n.Load()) / 2

	if r, ok := db.(interface{ Reset(context.Context) error }); ok {
		if err := r.Reset(ctx); err != nil {
			return rep, err
		}
		rep.IngestWorkers = rep.Workers
		eps, err := Ingest(ctx, w, cfg, db, rep.IngestWorkers)
		if err != nil {
			return rep, err
		}
		rep.IngestEventsPerS = eps
	}

	taken, seats, err := SeatRace(ctx, db, w.Cfg.Start, 10, 50)
	if err != nil {
		return rep, err
	}
	rep.SeatsTaken, rep.SeatsOnLicence = taken, seats
	rep.SeatRace = fmt.Sprintf("%d students joined a %d-seat licence at once: %d seats taken", 50, seats, taken)
	return rep, nil
}

// SeatRace has n students join a licence with seats seats at the same moment and returns the seats taken.
func SeatRace(ctx context.Context, st store.Store, at time.Time, seats, n int) (int, int, error) {
	l := store.Licence{ID: "lic_race", Code: "race-2026", Seats: seats, Starts: at, Ends: at.Add(365 * 24 * time.Hour),
		Customer: "race"}
	if err := st.PutLicence(ctx, l); err != nil {
		return 0, 0, err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var unexpected error
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := st.JoinCampus(ctx, l.Code, int64(800000+i), at.Add(time.Minute))
			if err != nil && !errors.Is(err, store.ErrFull) {
				mu.Lock()
				unexpected = err
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if unexpected != nil {
		return 0, seats, unexpected
	}
	taken, err := st.SeatCount(ctx, l.ID)
	return taken, seats, err
}

// Ingest applies every webhook delivery as fast as the ledger takes them, with workers that each own a share of
// the objects (so one object's events stay in order, as a queue partitioned by object id would keep them).
func Ingest(ctx context.Context, w *world.World, cfg ShopConfig, st store.Store, workers int) (float64, error) {
	end := w.End
	var calls atomic.Int64
	proc := &ingest.Processor{Store: st, API: api{w, func() time.Time { return end }, &calls}, Policy: cfg.Policy(),
		Plans: cfg.PlanMap(), Design: design(Proposed).Design}
	parts := make([][]world.Arrival, workers)
	for _, a := range w.Arrivals {
		h := fnv.New32a()
		h.Write([]byte(a.Event.Object.ID))
		i := int(h.Sum32() % uint32(workers))
		parts[i] = append(parts[i], a)
	}
	t0 := time.Now()
	var wg sync.WaitGroup
	errc := make(chan error, workers)
	for _, part := range parts {
		wg.Add(1)
		go func(part []world.Arrival) {
			defer wg.Done()
			for _, a := range part {
				if _, err := proc.Handle(ctx, a.Event, a.At); err != nil {
					errc <- err
					return
				}
			}
		}(part)
	}
	wg.Wait()
	close(errc)
	if err := <-errc; err != nil {
		return 0, err
	}
	return float64(len(w.Arrivals)) / time.Since(t0).Seconds(), nil
}
