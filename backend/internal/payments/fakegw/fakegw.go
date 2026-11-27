// Package fakegw is a local stand-in for a hosted payment gateway, for
// development and tests. It behaves like bKash Tokenized Checkout where it
// matters: money moves only when the merchant calls Complete, a payment can
// be completed at most once (a failed completion closes it), and refunds are
// idempotent. Its hosted page lets a developer approve, decline or cancel.
package fakegw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sync"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/payments"
)

// ErrNotFound means the gateway has no payment with that reference.
var ErrNotFound = errors.New("fakegw: no such payment")

// Gateway is the fake provider. BaseURL is the public origin serving its
// hosted page, for example "http://localhost:8088".
type Gateway struct {
	DB      dbx.DB
	BaseURL string

	mu   sync.Mutex
	fail map[string]error
}

// FailNext makes the next call of op ("start", "complete", "query" or
// "refund") return err, to rehearse timeouts and outages.
func (g *Gateway) FailNext(op string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail == nil {
		g.fail = map[string]error{}
	}
	g.fail[op] = err
}

func (g *Gateway) take(op string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	err := g.fail[op]
	delete(g.fail, op)
	return err
}

func random(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (g *Gateway) Provider() string         { return "fake" }
func (g *Gateway) CapturesOnComplete() bool { return true }

func (g *Gateway) Start(ctx context.Context, req payments.StartRequest) (payments.Started, error) {
	if err := g.take("start"); err != nil {
		return payments.Started{}, err
	}
	ref := random("FAKE-")
	if _, err := g.DB.Exec(ctx, `INSERT INTO fake_gateway_payment (ref, merchant_ref, amount_minor, currency, return_url)
		VALUES ($1, $2, $3, $4, $5)`, ref, req.MerchantRef, req.AmountMinor, req.Currency, req.ReturnURL); err != nil {
		return payments.Started{}, err
	}
	return payments.Started{ProviderRef: ref, RedirectURL: g.BaseURL + "/payments/fake/pay/" + ref}, nil
}

func (g *Gateway) ParseReturn(q url.Values) (string, payments.Outcome, error) {
	ref, outcome := q.Get("ref"), payments.Outcome(q.Get("status"))
	switch {
	case ref == "":
		return "", "", errors.New("fakegw: return without ref")
	case outcome != payments.OutcomeSuccess && outcome != payments.OutcomeFailure && outcome != payments.OutcomeCancel:
		return "", "", fmt.Errorf("fakegw: unknown status %q", outcome)
	}
	return ref, outcome, nil
}

type row struct {
	Ref         string  `db:"ref"`
	MerchantRef string  `db:"merchant_ref"`
	AmountMinor int64   `db:"amount_minor"`
	Currency    string  `db:"currency"`
	ReturnURL   string  `db:"return_url"`
	State       string  `db:"state"`
	TrxID       *string `db:"trx_id"`
	Refunded    int64   `db:"refunded_minor"`
}

func load(ctx context.Context, db dbx.DB, ref, suffix string) (row, error) {
	r, err := dbx.One[row](ctx, db, `SELECT ref, merchant_ref, amount_minor, currency, return_url, state, trx_id, refunded_minor
		FROM fake_gateway_payment WHERE ref = $1`+suffix, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return row{}, ErrNotFound
	}
	return r, err
}

var statusOf = map[string]payments.Status{
	"created":   payments.Pending,
	"approved":  payments.Authorized,
	"captured":  payments.Succeeded,
	"declined":  payments.Failed,
	"cancelled": payments.Failed,
	"failed":    payments.Failed,
}

func result(r row) payments.Result {
	res := payments.Result{Status: statusOf[r.State], ProviderRef: r.Ref, AmountMinor: r.AmountMinor,
		Currency: r.Currency, MerchantRef: r.MerchantRef}
	if r.TrxID != nil {
		res.TrxID = *r.TrxID
	}
	return res
}

// Complete captures an approved payment. Like bKash Execute, any other state
// closes the payment for good.
func (g *Gateway) Complete(ctx context.Context, ref string) (payments.Result, error) {
	if err := g.take("complete"); err != nil {
		return payments.Result{}, err
	}
	var out row
	err := dbx.InTx(ctx, g.DB, func(tx pgx.Tx) error {
		r, err := load(ctx, tx, ref, " FOR UPDATE")
		if err != nil {
			return err
		}
		switch r.State {
		case "approved":
			trx := random("FAKETRX-")
			r.State, r.TrxID = "captured", &trx
		case "captured":
		default:
			r.State = "failed"
		}
		out = r
		_, err = tx.Exec(ctx, `UPDATE fake_gateway_payment SET state = $2, trx_id = $3 WHERE ref = $1`, ref, r.State, r.TrxID)
		return err
	})
	if err != nil {
		return payments.Result{}, err
	}
	return result(out), nil
}

func (g *Gateway) Query(ctx context.Context, ref string) (payments.Result, error) {
	if err := g.take("query"); err != nil {
		return payments.Result{}, err
	}
	r, err := load(ctx, g.DB, ref, "")
	if err != nil {
		return payments.Result{}, err
	}
	return result(r), nil
}

// Refund refunds a captured payment, once per refund reference.
func (g *Gateway) Refund(ctx context.Context, req payments.RefundRequest) (payments.RefundResult, error) {
	if err := g.take("refund"); err != nil {
		return payments.RefundResult{}, err
	}
	var out payments.RefundResult
	err := dbx.InTx(ctx, g.DB, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `SELECT refund_id FROM fake_gateway_refund WHERE refund_ref = $1`, req.RefundRef).Scan(&id)
		if err == nil {
			out = payments.RefundResult{Status: payments.RefundCompleted, ProviderRefundID: id}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		r, err := load(ctx, tx, req.ProviderRef, " FOR UPDATE")
		if err != nil {
			return err
		}
		if r.State != "captured" || r.TrxID == nil || *r.TrxID != req.TrxID || r.Refunded+req.AmountMinor > r.AmountMinor {
			out = payments.RefundResult{Status: payments.RefundFailed, Detail: "refund amount not valid"}
			return nil
		}
		id = random("FAKERF-")
		if _, err := tx.Exec(ctx, `INSERT INTO fake_gateway_refund (refund_ref, payment_ref, amount_minor, refund_id)
			VALUES ($1, $2, $3, $4)`, req.RefundRef, r.Ref, req.AmountMinor, id); err != nil {
			return err
		}
		out = payments.RefundResult{Status: payments.RefundCompleted, ProviderRefundID: id}
		_, err = tx.Exec(ctx, `UPDATE fake_gateway_payment SET refunded_minor = refunded_minor + $2 WHERE ref = $1`,
			r.Ref, req.AmountMinor)
		return err
	})
	return out, err
}

var decisions = map[string]struct{ state, outcome string }{
	"approve": {"approved", "success"},
	"decline": {"declined", "failure"},
	"cancel":  {"cancelled", "cancel"},
}

// Decide records the buyer's choice on the hosted page: "approve",
// "decline" or "cancel". It returns where to send the buyer back to.
func (g *Gateway) Decide(ctx context.Context, ref, action string) (string, error) {
	d, ok := decisions[action]
	if !ok {
		return "", fmt.Errorf("fakegw: unknown action %q", action)
	}
	var returnURL string
	err := g.DB.QueryRow(ctx, `UPDATE fake_gateway_payment SET state = $2 WHERE ref = $1 AND state = 'created'
		RETURNING return_url`, ref, d.state).Scan(&returnURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(returnURL)
	q := u.Query()
	q.Set("ref", ref)
	q.Set("status", d.outcome)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

var page = template.Must(template.New("pay").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Fake payment gateway</title>
<style>body{font-family:system-ui,sans-serif;max-width:28rem;margin:2rem auto;padding:0 1rem}
button{display:block;width:100%;min-height:3rem;margin:.5rem 0;font:inherit;border-radius:999px;border:1px solid #888}
.pay{background:#0b6e4f;color:#fff;border:0}</style></head>
<body><h1>Fake payment gateway</h1>
<p>Development only. No real money moves.</p>
<p>Amount: <strong>{{.Amount}} {{.Currency}}</strong><br>Reference: {{.MerchantRef}}</p>
<form method="post">
<button class="pay" name="action" value="approve">Approve payment</button>
<button name="action" value="decline">Decline (insufficient balance)</button>
<button name="action" value="cancel">Cancel and return</button>
</form></body></html>`))

// ServeHTTP serves the hosted page at /payments/fake/pay/{ref}.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	if r.Method == http.MethodPost {
		next, err := g.Decide(r.Context(), ref, r.FormValue("action"))
		if err != nil {
			http.Error(w, "This payment cannot be changed.", http.StatusConflict)
			return
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	p, err := load(r.Context(), g.DB, ref, " AND state = 'created'")
	if err != nil {
		http.Error(w, "This payment is no longer open.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = page.Execute(w, map[string]string{
		"Amount":      fmt.Sprintf("%d.%02d", p.AmountMinor/100, p.AmountMinor%100),
		"Currency":    p.Currency,
		"MerchantRef": p.MerchantRef,
	})
}
