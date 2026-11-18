package shop

import (
	"testing"
	"time"
)

func TestAccessUntil(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := t0.Add(30 * 24 * time.Hour)
	p := Policy{Grace: 7 * 24 * time.Hour, RenewalBuffer: 30 * time.Hour, Recurring: map[string]bool{"monthly": true}}
	base := Sub{Plan: "monthly", StartedAt: t0, PeriodStart: t0, PeriodEnd: end}
	for _, tc := range []struct {
		name string
		mod  func(*Sub)
		want time.Time
	}{
		{"active renewing: buffer", func(s *Sub) { s.Status = Active }, end.Add(30 * time.Hour)},
		{"set to end: no buffer", func(s *Sub) { s.Status, s.CancelAtPeriodEnd = Active, true }, end},
		{"pass: no buffer", func(s *Sub) { s.Status, s.Plan = Active, "exam_pass" }, end},
		{"past due: grace", func(s *Sub) { s.Status = PastDue }, t0.Add(7 * 24 * time.Hour)},
		{"past due: grace capped at period end", func(s *Sub) { s.Status, s.PeriodEnd = PastDue, t0.Add(48*time.Hour) }, t0.Add(48 * time.Hour)},
		{"refunded early", func(s *Sub) { s.Status, s.EndedAt = Canceled, t0.Add(time.Hour) }, t0.Add(time.Hour)},
		{"cancelled at period end", func(s *Sub) { s.Status = Canceled }, end},
		{"expired", func(s *Sub) { s.Status, s.EndedAt = Expired, t0.Add(9*24*time.Hour) }, t0.Add(9 * 24 * time.Hour)},
	} {
		s := base
		tc.mod(&s)
		if got := AccessUntil(s, p); !got.Equal(tc.want) {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	if Grants(base, p, t0.Add(-time.Second)) {
		t.Error("no access before the start")
	}
}
