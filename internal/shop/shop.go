// Package shop holds the shop's domain: what a payment provider tells us about a subscription, and the rule that
// turns that into access to the platform.
//
// A subscription is known to us only as snapshots the providers send (webhooks) or return (API reads). The access a
// snapshot grants is a function of the snapshot and the policy (grace after a failed renewal), so the ledger never
// accumulates "extend by a month" steps: it stores the latest correct snapshot of every object and derives access.
package shop

import (
	"fmt"
	"time"
)

// Provider is a payment provider.
type Provider string

const (
	Stripe    Provider = "stripe"
	Chargebee Provider = "chargebee"
)

// Status of a subscription or pass as the provider reports it.
type Status string

const (
	Trialing Status = "trialing"
	Active   Status = "active"
	PastDue  Status = "past_due" // a renewal failed; the provider retries (dunning)
	Canceled Status = "canceled" // ends at PeriodEnd, or at EndedAt when ended early (refund, dispute)
	Expired  Status = "expired"  // dunning gave up, or a pass ran out
)

// Sub is a provider's subscription or one-time pass, as one snapshot.
type Sub struct {
	ID                string    `json:"id"`
	Provider          Provider  `json:"provider"`
	User              int64     `json:"user"`
	Plan              string    `json:"plan"`
	Status            Status    `json:"status"`
	StartedAt         time.Time `json:"started_at"`
	PeriodStart       time.Time `json:"period_start"`
	PeriodEnd         time.Time `json:"period_end"`
	CancelAtPeriodEnd bool      `json:"cancel_at_period_end"`
	EndedAt           time.Time `json:"ended_at,omitempty"`
	// Version orders snapshots of one object. Chargebee sends it (resource_version); Stripe does not, so the
	// gateway orders a Stripe snapshot by the event's creation time, or by the time it read the object back.
	Version int64 `json:"version"`
}

// Event is one webhook delivery. The same event can arrive twice, late, or never.
type Event struct {
	ID       string    `json:"id"`
	Provider Provider  `json:"provider"`
	Type     string    `json:"type"`
	Created  time.Time `json:"created"`
	Object   Sub       `json:"object"`
}

// Event types the shop handles.
const (
	CheckoutCompleted = "checkout.completed"
	InvoicePaid       = "invoice.paid"
	PaymentFailed     = "invoice.payment_failed"
	SubUpdated        = "subscription.updated"
	SubCanceled       = "subscription.canceled"
	ChargeRefunded    = "charge.refunded"
	DisputeCreated    = "charge.dispute.created"
)

// Policy is the contract with the customer: how long access survives a failed renewal, and how long the gateway
// keeps a renewing subscription open while the renewal's webhook is on its way.
type Policy struct {
	Grace         time.Duration
	RenewalBuffer time.Duration   // added to PeriodEnd of an active, renewing subscription (never to a pass)
	Recurring     map[string]bool // plans that renew
}

// AccessUntil is the end of the access a snapshot grants; access starts at StartedAt.
func AccessUntil(s Sub, p Policy) time.Time {
	switch s.Status {
	case Trialing, Active:
		if p.RenewalBuffer > 0 && !s.CancelAtPeriodEnd && p.Recurring[s.Plan] {
			return s.PeriodEnd.Add(p.RenewalBuffer)
		}
		return s.PeriodEnd
	case PastDue:
		end := s.PeriodStart.Add(p.Grace)
		if end.After(s.PeriodEnd) {
			end = s.PeriodEnd
		}
		return end
	case Canceled:
		if !s.EndedAt.IsZero() {
			return s.EndedAt
		}
		return s.PeriodEnd
	default:
		if !s.EndedAt.IsZero() {
			return s.EndedAt
		}
		return s.StartedAt
	}
}

// Grants reports whether the snapshot gives access at t.
func Grants(s Sub, p Policy, t time.Time) bool {
	return !t.Before(s.StartedAt) && t.Before(AccessUntil(s, p))
}

// Plan is a product the shop sells.
type Plan struct {
	ID          string        `json:"id"`
	Period      time.Duration `json:"period"`
	PriceCents  int64         `json:"price_cents"` // net, before VAT
	Recurring   bool          `json:"recurring"`
	TrialPeriod time.Duration `json:"trial_period"`
}

// DailyValueCents is what a day of the plan is worth: used to price access given away by mistake.
func (p Plan) DailyValueCents() float64 {
	return float64(p.PriceCents) / (p.Period.Hours() / 24)
}

// Reason explains one source of access, for support and for the API.
type Reason struct {
	Source   string    `json:"source"` // "stripe:sub_123", "campus:charite-2026"
	Status   string    `json:"status"`
	Until    time.Time `json:"until"`
	Event    string    `json:"event"` // the event or read that set this state
	Grants   bool      `json:"grants"`
	Describe string    `json:"describe"`
}

func (r Reason) String() string {
	return fmt.Sprintf("%s %s until %s", r.Source, r.Status, r.Until.Format(time.RFC3339))
}
