package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
)

// Memory is the ledger in memory, with the same contract as MySQL. Used by the simulation, which applies hundreds
// of thousands of events per design; the replay test shows both stores give the same answers.
type Memory struct {
	mu        sync.Mutex
	processed map[string]bool
	subs      map[string]Record
	byUser    map[int64][]string
	history   map[string][]Record
	licences  map[string]Licence // by code
	seats     map[string]map[int64]time.Time
	userSeats map[int64][]string
}

func NewMemory() *Memory {
	return &Memory{processed: map[string]bool{}, subs: map[string]Record{}, byUser: map[int64][]string{},
		history: map[string][]Record{}, licences: map[string]Licence{}, seats: map[string]map[int64]time.Time{},
		userSeats: map[int64][]string{}}
}

func (m *Memory) Apply(_ context.Context, c Change) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.EventID != "" {
		key := string(c.Provider) + "/" + c.EventID
		if m.processed[key] {
			return ErrDuplicate
		}
		m.processed[key] = true
	}
	if c.Sub == nil {
		return nil
	}
	old, ok := m.subs[c.Sub.ID]
	if c.Ordered && ok && old.Sub.Version >= c.Sub.Version {
		return ErrStale
	}
	r := Record{Sub: *c.Sub, Until: c.Until, Source: c.Source, At: c.At}
	if !ok {
		m.byUser[c.Sub.User] = append(m.byUser[c.Sub.User], c.Sub.ID)
	}
	m.subs[c.Sub.ID] = r
	m.history[c.Sub.ID] = append(m.history[c.Sub.ID], r)
	return nil
}

func (m *Memory) Sub(_ context.Context, id string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.subs[id]
	return r, ok, nil
}

func (m *Memory) SubsOfUser(_ context.Context, user int64) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, 0, len(m.byUser[user]))
	for _, id := range m.byUser[user] {
		out = append(out, m.subs[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sub.ID < out[j].Sub.ID })
	return out, nil
}

func (m *Memory) Subs(_ context.Context, p shop.Provider) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.subs {
		if r.Sub.Provider == p {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sub.ID < out[j].Sub.ID })
	return out, nil
}

func (m *Memory) History(_ context.Context, id string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.history[id]...), nil
}

func (m *Memory) Access(_ context.Context, user int64, t time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.byUser[user] {
		r := m.subs[id]
		if !t.Before(r.Sub.StartedAt) && t.Before(r.Until) {
			return true, nil
		}
	}
	for _, code := range m.userSeats[user] {
		l := m.licences[code]
		if !t.Before(l.Starts) && t.Before(l.Ends) && !t.Before(m.seats[code][user]) {
			return true, nil
		}
	}
	return false, nil
}

func (m *Memory) PutLicence(_ context.Context, l Licence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.licences[l.Code] = l
	if m.seats[l.Code] == nil {
		m.seats[l.Code] = map[int64]time.Time{}
	}
	return nil
}

func (m *Memory) JoinCampus(_ context.Context, code string, user int64, at time.Time) (Licence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.licences[code]
	if !ok {
		return Licence{}, ErrUnknownCode
	}
	if at.Before(l.Starts) || !at.Before(l.Ends) {
		return l, ErrNotActive
	}
	if _, has := m.seats[code][user]; has {
		return l, nil
	}
	if len(m.seats[code]) >= l.Seats {
		return l, ErrFull
	}
	m.seats[code][user] = at
	m.userSeats[user] = append(m.userSeats[user], code)
	return l, nil
}

func (m *Memory) SeatsOf(_ context.Context, user int64) ([]Licence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Licence
	for _, c := range m.userSeats[user] {
		out = append(out, m.licences[c])
	}
	return out, nil
}

func (m *Memory) SeatCount(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for code, l := range m.licences {
		if l.ID == id {
			return len(m.seats[code]), nil
		}
	}
	return 0, nil
}

func (m *Memory) Close() error { return nil }
