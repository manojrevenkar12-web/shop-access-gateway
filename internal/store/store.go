// Package store keeps the entitlement ledger: the latest snapshot of every subscription and pass, an append-only
// history of every state applied and why, the webhook events already processed, and campus licences with seats.
//
// Two implementations with one contract: MySQL in production, memory for fast simulation. A conformance test runs
// the same scenarios against both and a replay test feeds both the same event stream and compares every answer.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
)

var (
	ErrDuplicate   = errors.New("event already processed")
	ErrStale       = errors.New("an equal or newer snapshot is already stored")
	ErrUnknownCode = errors.New("no campus licence with that code")
	ErrFull        = errors.New("every seat of the licence is taken")
	ErrNotActive   = errors.New("the licence is not active at that time")
)

// Change is one atomic ledger write.
type Change struct {
	Provider shop.Provider
	EventID  string    // marks the event processed in the same transaction; empty: no de-duplication
	Sub      *shop.Sub // the snapshot to store
	Until    time.Time // access the snapshot grants (computed by the caller from the policy)
	Ordered  bool      // refuse a snapshot that is not newer than the stored one
	Source   string    // why: "webhook evt_1", "refetch", "reconcile", "return", "support T-17"
	At       time.Time // when the platform applied it
}

// Record is a stored snapshot with the access it grants and the write that put it there.
type Record struct {
	Sub    shop.Sub
	Until  time.Time
	Source string
	At     time.Time
}

// Licence is a campus licence: a university buys seats for its students for a period.
type Licence struct {
	ID       string
	Code     string
	Seats    int
	Starts   time.Time
	Ends     time.Time
	Customer string
}

// Store is the ledger.
type Store interface {
	// Apply writes a change atomically. It returns ErrDuplicate when the event was processed before and ErrStale
	// when Ordered and the stored snapshot is as new; in both cases nothing changes except the stale event being
	// marked processed.
	Apply(ctx context.Context, c Change) error
	Sub(ctx context.Context, id string) (Record, bool, error)
	SubsOfUser(ctx context.Context, user int64) ([]Record, error)
	Subs(ctx context.Context, provider shop.Provider) ([]Record, error)
	History(ctx context.Context, subID string) ([]Record, error)
	// Access reports whether any stored snapshot or campus seat grants the user access at t.
	Access(ctx context.Context, user int64, t time.Time) (bool, error)

	PutLicence(ctx context.Context, l Licence) error
	// JoinCampus takes a seat if one is free: concurrent joins never take more seats than the licence has.
	JoinCampus(ctx context.Context, code string, user int64, at time.Time) (Licence, error)
	SeatsOf(ctx context.Context, user int64) ([]Licence, error)
	SeatCount(ctx context.Context, licenceID string) (int, error)

	Close() error
}
