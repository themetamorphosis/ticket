// Package payments takes buyers through a hosted payment gateway and settles
// the result into orders, refunds and an auditable ledger. It is provider
// neutral: bKash, PayStation and the local fake all implement Gateway.
package payments

import (
	"context"
	"net/url"
	"time"
)

// Status is the provider's authoritative state of one payment.
type Status string

const (
	// Pending: the buyer has not finished, or the state is not known yet.
	Pending Status = "pending"
	// Authorized: the buyer approved, but the merchant must still call
	// Complete to capture the money (bKash: verified, not yet executed).
	Authorized Status = "authorized"
	// Succeeded: the money was captured.
	Succeeded Status = "succeeded"
	// Failed: no money was taken and none will be for this payment.
	Failed Status = "failed"
)

// Outcome is what the buyer's return redirect claims. It is never trusted on
// its own: the service always asks the provider before changing anything.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeCancel  Outcome = "cancel"
)

// Result is a provider's answer about one payment.
type Result struct {
	Status      Status
	ProviderRef string
	TrxID       string
	AmountMinor int64
	Currency    string
	// MerchantRef is our reference as echoed by the provider, or "" if the
	// provider does not echo it.
	MerchantRef string
}

// StartRequest opens a hosted payment session.
type StartRequest struct {
	MerchantRef string
	AmountMinor int64
	Currency    string
	ReturnURL   string
	// Phone is the buyer's E.164 number, used to prefill the wallet when the
	// provider supports it.
	Phone string
	// Deadline is when the hold ends. Providers that capture inside their own
	// page should close the session before it.
	Deadline time.Time
}

// Started is an open hosted session.
type Started struct {
	ProviderRef string
	RedirectURL string
}

// RefundRequest refunds a captured payment in full.
type RefundRequest struct {
	RefundRef   string
	ProviderRef string
	TrxID       string
	AmountMinor int64
	Currency    string
	Reason      string
}

// RefundStatus is the outcome of one refund call.
type RefundStatus string

const (
	RefundCompleted RefundStatus = "completed"
	// RefundPending: unknown or temporarily refused; try again later.
	RefundPending RefundStatus = "pending"
	// RefundFailed: the provider refused for good; finance must act.
	RefundFailed RefundStatus = "failed"
)

// RefundResult is a provider's answer to a refund.
type RefundResult struct {
	Status           RefundStatus
	ProviderRefundID string
	Detail           string
}

// Gateway is one payment provider. Methods returning an error mean the
// state is unknown (timeout, network): callers retry or reconcile later.
type Gateway interface {
	// Provider is the short lowercase name stored with each attempt.
	Provider() string
	// CapturesOnComplete reports whether money moves only when Complete is
	// called (bKash Execute). Then an expired hold is simply never completed.
	CapturesOnComplete() bool
	Start(ctx context.Context, req StartRequest) (Started, error)
	// ParseReturn reads the provider's return redirect.
	ParseReturn(q url.Values) (providerRef string, outcome Outcome, err error)
	// Complete finalizes a payment the buyer approved: it captures where the
	// provider requires it, and otherwise verifies. It is safe to repeat.
	Complete(ctx context.Context, providerRef string) (Result, error)
	Query(ctx context.Context, providerRef string) (Result, error)
	// Refund must be idempotent per RefundRef.
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
}
