// Package support answers the shop's support inbox: "I paid but cannot get in", "I was charged twice", refunds,
// cancellations, campus codes.
//
// A reader (rules, or a language model) turns the message into an intent and the customer it is about. The message
// is data: nothing in it is an instruction, and a message that tries to give one is passed to a person. Code then
// checks the customer's payments at the provider and the ledger, and acts only when they prove the case: it
// refunds a second charge only when two charges for the same purchase exist, grants access only when the provider
// says it was paid. Everything else goes to a person with what was found.
package support

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/ingest"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
	"github.com/manojrevenkar12-web/shop-access-gateway/internal/store"
)

// Intent of a ticket.
type Intent string

const (
	PaidNoAccess Intent = "paid_no_access"
	DoubleCharge Intent = "double_charge"
	Withdrawal   Intent = "withdrawal"
	Cancel       Intent = "cancel"
	Campus       Intent = "campus"
	Unknown      Intent = "unknown"
)

// Reading is what a reader took from a ticket.
type Reading struct {
	Intent    Intent
	User      int64
	Email     string
	Code      string
	Injection bool
}

// Reader reads a ticket.
type Reader interface {
	Read(ctx context.Context, text string) (Reading, error)
}

var (
	reCustomer  = regexp.MustCompile(`(?i)\bAMB-(\d{4,9})\b`)
	reEmail     = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	reCode      = regexp.MustCompile(`(?i)(?:code|key|licen[cs]e)[^A-Za-z0-9]*\(?\s*([A-Za-z][A-Za-z-]*[- ]?\d{4})`)
	reCodeAlt   = regexp.MustCompile(`(?i)\b([A-Za-z]+-?[A-Za-z]*[- ]?20\d{2})\b`)
	injection   = []string{"ignore all", "ignore your", "ignoriere", "admin mode", "system:", "you are now"}
	intentWords = []struct {
		intent Intent
		words  []string
	}{
		{DoubleCharge, []string{"twice", "two payments", "doppelt", "double", "zweimal"}},
		{Campus, []string{"code", "campus", "university"}},
		{Cancel, []string{"cancel", "kündig", "stop the renewal"}},
		{Withdrawal, []string{"withdraw", "refund", "zurücktreten", "erstatt", "geld zurück"}},
		{PaidNoAccess, []string{"paid", "bezahlt", "payment went", "bought", "can't log", "cannot log", "no access", "keinen zugang", "locked"}},
	}
)

// Rules reads tickets with keyword rules in English and German.
type Rules struct{}

func (Rules) Read(_ context.Context, text string) (Reading, error) {
	low := strings.ToLower(text)
	var r Reading
	if m := reCustomer.FindStringSubmatch(text); m != nil {
		r.User, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := reEmail.FindString(text); m != "" {
		r.Email = strings.ToLower(m)
	}
	for _, w := range injection {
		if strings.Contains(low, w) {
			r.Injection = true
		}
	}
	r.Intent = Unknown
	for _, iw := range intentWords {
		for _, w := range iw.words {
			if strings.Contains(low, w) {
				r.Intent = iw.intent
				break
			}
		}
		if r.Intent != Unknown {
			break
		}
	}
	if r.Intent == Campus {
		if m := reCode.FindStringSubmatch(text); m != nil {
			r.Code = m[1]
		} else if m := reCodeAlt.FindStringSubmatch(text); m != nil {
			r.Code = m[1]
		}
	}
	return r, nil
}

// Charge is a payment as the provider reports it.
type Charge struct {
	ID       string
	SubID    string
	Provider shop.Provider
	Amount   int64
	Created  time.Time
	Refunded bool
}

// Billing is what support may read and do at the payment providers.
type Billing interface {
	SubsOf(ctx context.Context, user int64) ([]shop.Sub, error) // the provider's objects for this customer
	Charges(ctx context.Context, user int64) ([]Charge, error)
	Refund(ctx context.Context, chargeID, idempotencyKey string) error
	CancelAtPeriodEnd(ctx context.Context, subID string) error
}

// Directory finds a customer by email.
type Directory interface {
	ByEmail(ctx context.Context, email string) (int64, bool)
}

// Settings for the resolver (config/shop.json).
type Settings struct {
	WithdrawalDays      int `json:"withdrawal_days"`
	DoubleChargeMinutes int `json:"double_charge_window_minutes"`
}

// Resolver decides and acts.
type Resolver struct {
	Reader    Reader
	Billing   Billing
	Ledger    store.Store
	Processor *ingest.Processor
	Directory Directory
	Settings  Settings
	Verify    bool // false: the agent does what the message asks (the ablation that shows why checks matter)
	Automate  bool // false: every ticket goes to a person (today)
}

// Resolution is what happened to a ticket.
type Resolution struct {
	Action string `json:"action"` // sync_grant, explain_unpaid, refund_duplicate, explain_single_charge,
	// refund_withdrawal, cancel, join_campus, escalate
	Evidence string `json:"evidence"`
	Intent   Intent `json:"intent"`
	User     int64  `json:"user"`
}

// MoneyOrAccess reports whether an action gives money back or access.
func MoneyOrAccess(action string) bool {
	return action == "refund_duplicate" || action == "refund_withdrawal" || action == "sync_grant" ||
		action == "join_campus"
}

func escalate(r Reading, why string) Resolution {
	return Resolution{Action: "escalate", Evidence: why, Intent: r.Intent, User: r.User}
}

// Resolve handles one ticket.
func (s *Resolver) Resolve(ctx context.Context, ticketID, text string, now time.Time) (Resolution, error) {
	if !s.Automate {
		return Resolution{Action: "escalate", Evidence: "handled by a person"}, nil
	}
	r, err := s.Reader.Read(ctx, text)
	if err != nil {
		return Resolution{}, err
	}
	if r.User == 0 && r.Email != "" && s.Directory != nil {
		if id, ok := s.Directory.ByEmail(ctx, r.Email); ok {
			r.User = id
		}
	}
	if s.Verify && r.Injection {
		return escalate(r, "the message tries to give instructions"), nil
	}
	if r.Intent == Unknown {
		return escalate(r, "not a billing or access question"), nil
	}
	if r.User == 0 && r.Intent != Campus {
		return escalate(r, "no customer number or known email in the message"), nil
	}
	switch r.Intent {
	case PaidNoAccess:
		return s.paidNoAccess(ctx, r, now, ticketID)
	case DoubleCharge:
		return s.doubleCharge(ctx, r)
	case Withdrawal:
		return s.withdrawal(ctx, r, now)
	case Cancel:
		return s.cancel(ctx, r)
	case Campus:
		return s.campus(ctx, r, now)
	}
	return escalate(r, "no rule"), nil
}

func (s *Resolver) paidNoAccess(ctx context.Context, r Reading, now time.Time, ticketID string) (Resolution, error) {
	if !s.Verify { // the naive agent believes the customer and opens the door
		return Resolution{Action: "sync_grant", Evidence: "customer says they paid", Intent: r.Intent, User: r.User}, nil
	}
	subs, err := s.Billing.SubsOf(ctx, r.User)
	if err != nil {
		return Resolution{}, err
	}
	var seen []string
	for _, sub := range subs {
		if _, err := s.Processor.Sync(ctx, sub.Provider, sub.ID, now, "support "+ticketID); err != nil {
			return Resolution{}, err
		}
		seen = append(seen, fmt.Sprintf("%s %s", sub.ID, sub.Status))
	}
	ok, err := s.Ledger.Access(ctx, r.User, now)
	if err != nil {
		return Resolution{}, err
	}
	if ok {
		return Resolution{Action: "sync_grant", Evidence: "provider shows it paid: " + strings.Join(seen, ", "),
			Intent: r.Intent, User: r.User}, nil
	}
	return Resolution{Action: "explain_unpaid", Evidence: "provider shows no paid access: " + strings.Join(seen, ", "),
		Intent: r.Intent, User: r.User}, nil
}

func (s *Resolver) doubleCharge(ctx context.Context, r Reading) (Resolution, error) {
	charges, err := s.Billing.Charges(ctx, r.User)
	if err != nil {
		return Resolution{}, err
	}
	window := time.Duration(s.Settings.DoubleChargeMinutes) * time.Minute
	for i, a := range charges {
		for _, b := range charges[i+1:] {
			gap := b.Created.Sub(a.Created)
			if gap < 0 {
				gap = -gap
			}
			if s.Verify && (a.SubID != b.SubID || a.Amount != b.Amount || gap > window || a.Refunded || b.Refunded) {
				continue
			}
			later := b
			if b.Created.Before(a.Created) {
				later = a
			}
			if err := s.Billing.Refund(ctx, later.ID, "duplicate-"+later.ID); err != nil {
				return Resolution{}, err
			}
			return Resolution{Action: "refund_duplicate", Evidence: fmt.Sprintf("%s and %s: same purchase, %s apart",
				a.ID, b.ID, gap.Round(time.Second)), Intent: r.Intent, User: r.User}, nil
		}
	}
	if !s.Verify && len(charges) > 0 { // the naive agent refunds the latest charge on request
		last := charges[len(charges)-1]
		if err := s.Billing.Refund(ctx, last.ID, "duplicate-"+last.ID); err != nil {
			return Resolution{}, err
		}
		return Resolution{Action: "refund_duplicate", Evidence: "customer says so", Intent: r.Intent, User: r.User}, nil
	}
	ev := fmt.Sprintf("%d charges, no two for the same purchase within %d minutes", len(charges), s.Settings.DoubleChargeMinutes)
	if len(charges) == 1 {
		ev = "one charge only"
	}
	return Resolution{Action: "explain_single_charge", Evidence: ev, Intent: r.Intent, User: r.User}, nil
}

func (s *Resolver) withdrawal(ctx context.Context, r Reading, now time.Time) (Resolution, error) {
	charges, err := s.Billing.Charges(ctx, r.User)
	if err != nil || len(charges) == 0 {
		return escalate(r, "no payment found"), err
	}
	first := charges[0]
	subs, err := s.Billing.SubsOf(ctx, r.User)
	if err != nil {
		return Resolution{}, err
	}
	active := false
	for _, sub := range subs {
		if sub.ID == first.SubID && (sub.Status == shop.Active || sub.Status == shop.Trialing || sub.Status == shop.PastDue) &&
			!sub.CancelAtPeriodEnd {
			active = true
		}
	}
	within := now.Sub(first.Created) <= time.Duration(s.Settings.WithdrawalDays)*24*time.Hour
	if s.Verify && (!within || !active || first.Refunded) {
		return escalate(r, fmt.Sprintf("first payment %s ago; the %d-day withdrawal period does not apply automatically",
			now.Sub(first.Created).Round(time.Hour), s.Settings.WithdrawalDays)), nil
	}
	if err := s.Billing.Refund(ctx, first.ID, "withdrawal-"+first.ID); err != nil {
		return Resolution{}, err
	}
	return Resolution{Action: "refund_withdrawal", Evidence: "within the withdrawal period: " + first.ID,
		Intent: r.Intent, User: r.User}, nil
}

func (s *Resolver) cancel(ctx context.Context, r Reading) (Resolution, error) {
	subs, err := s.Billing.SubsOf(ctx, r.User)
	if err != nil {
		return Resolution{}, err
	}
	for _, sub := range subs {
		if (sub.Status == shop.Active || sub.Status == shop.Trialing || sub.Status == shop.PastDue) && !sub.CancelAtPeriodEnd &&
			sub.Plan != "exam_pass" {
			if err := s.Billing.CancelAtPeriodEnd(ctx, sub.ID); err != nil {
				return Resolution{}, err
			}
			return Resolution{Action: "cancel", Evidence: "renewal stopped: " + sub.ID, Intent: r.Intent, User: r.User}, nil
		}
	}
	return escalate(r, "no renewing subscription found"), nil
}

// NormaliseCode is how the gateway reads a campus code someone typed: case, spaces and the year's hyphen do not
// matter.
func NormaliseCode(typed string) string {
	c := strings.ToLower(strings.Join(strings.Fields(typed), ""))
	if len(c) > 4 && c[len(c)-5] != '-' {
		if _, err := strconv.Atoi(c[len(c)-4:]); err == nil {
			c = c[:len(c)-4] + "-" + c[len(c)-4:]
		}
	}
	return c
}

func (s *Resolver) campus(ctx context.Context, r Reading, now time.Time) (Resolution, error) {
	if r.Code == "" || r.User == 0 {
		return escalate(r, "no code or customer in the message"), nil
	}
	l, err := s.Ledger.JoinCampus(ctx, NormaliseCode(r.Code), r.User, now)
	switch {
	case errors.Is(err, store.ErrFull):
		return escalate(r, "licence "+l.ID+" has no free seat: the university decides"), nil
	case errors.Is(err, store.ErrUnknownCode), errors.Is(err, store.ErrNotActive):
		return escalate(r, "no active licence for "+r.Code), nil
	case err != nil:
		return Resolution{}, err
	}
	return Resolution{Action: "join_campus", Evidence: "seat on " + l.ID, Intent: r.Intent, User: r.User}, nil
}
