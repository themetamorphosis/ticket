// Package bkash implements payments.Gateway for bKash Tokenized Checkout,
// URL-based checkout without agreement (mode 0011), as documented at
// https://developer.bka.sh (v2 tokenized-checkout API).
//
// Money moves only when we call Execute, so the payments service simply
// never executes a payment whose hold has ended. The return redirect is not
// proof of payment: Execute or Query decides. Items still to confirm in the
// sandbox: whether Authorization needs a "Bearer " prefix (the docs' samples
// send the bare id_token, as here) and which trxId Refund Status expects.
package bkash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/payments"
)

// SandboxURL is the documented sandbox base URL. Production's comes from bKash.
const SandboxURL = "https://tokenized.sandbox.bka.sh/v2/tokenized-checkout"

// Config holds the merchant credentials. They come from the runtime
// environment and are never logged or stored outside gateway_token.
type Config struct {
	BaseURL   string
	Username  string
	Password  string
	AppKey    string
	AppSecret string
}

// Gateway talks to bKash. The access token is cached in PostgreSQL and shared
// by every process, because bKash allows two grants and two refreshes per hour.
type Gateway struct {
	cfg  Config
	db   dbx.DB
	http *http.Client
}

// New returns a bKash gateway. client should have a timeout above bKash's
// own 30-second default.
func New(cfg Config, db dbx.DB, client *http.Client) *Gateway {
	return &Gateway{cfg: cfg, db: db, http: client}
}

// ErrUnknown marks an outcome bKash did not report: a timeout, network error
// or server error. The payments service treats it as "ask again later".
var ErrUnknown = errors.New("bkash: outcome unknown")

// APIError is a definite error answer from bKash.
type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string { return "bkash: " + e.Code + " " + e.Message }

func (g *Gateway) Provider() string         { return "bkash" }
func (g *Gateway) CapturesOnComplete() bool { return true }

// ---------- HTTP ----------

type envelope struct {
	ExternalCode  string `json:"externalCode"`
	ErrorMessage  string `json:"errorMessageEn"`
	StatusCode    string `json:"statusCode"`
	StatusMessage string `json:"statusMessage"`
}

func (g *Gateway) call(ctx context.Context, path string, authed bool, body, out any) error {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.BaseURL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if authed {
		tok, err := g.token(ctx)
		if err != nil {
			// A token problem says nothing about this payment or refund.
			return fmt.Errorf("%w: token: %v", ErrUnknown, err)
		}
		req.Header.Set("Authorization", tok)
		req.Header.Set("X-App-Key", g.cfg.AppKey)
	} else {
		req.Header.Set("username", g.cfg.Username)
		req.Header.Set("password", g.cfg.Password)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnknown, path, redact(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode >= 500 {
		return fmt.Errorf("%w: %s: HTTP %d", ErrUnknown, path, resp.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%w: %s: unreadable response", ErrUnknown, path)
	}
	switch {
	case env.ExternalCode != "":
		return &APIError{Code: env.ExternalCode, Message: env.ErrorMessage}
	case env.StatusCode != "" && env.StatusCode != "0000":
		return &APIError{Code: env.StatusCode, Message: env.StatusMessage}
	case resp.StatusCode >= 300:
		return &APIError{Code: strconv.Itoa(resp.StatusCode), Message: http.StatusText(resp.StatusCode)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: %s: unexpected response shape", ErrUnknown, path)
	}
	return nil
}

// redact keeps query strings and credentials out of error text.
func redact(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Op + " " + strings.SplitN(uerr.URL, "?", 2)[0] + ": " + uerr.Err.Error()
	}
	return err.Error()
}

// ---------- Tokens ----------

type tokenResponse struct {
	IDToken      string  `json:"id_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    flexInt `json:"expires_in"`
}

// flexInt accepts 3600 or "3600"; bKash documents a string and sends a number.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	n, err := strconv.Atoi(strings.Trim(string(b), `"`))
	*f = flexInt(n)
	return err
}

// token returns a valid access token, refreshing or granting one if needed.
// One process at a time renews it, under a transaction-scoped advisory lock,
// so a fleet of nodes still makes one call.
func (g *Gateway) token(ctx context.Context) (string, error) {
	const fresh = `SELECT access_token FROM gateway_token WHERE provider = 'bkash' AND access_expires_at > now() + interval '5 minutes'`
	var tok string
	err := g.db.QueryRow(ctx, fresh).Scan(&tok)
	if !errors.Is(err, pgx.ErrNoRows) {
		return tok, err
	}
	err = dbx.InTx(ctx, g.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('gateway_token:bkash'))`); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, fresh).Scan(&tok)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var refresh string
		err = tx.QueryRow(ctx, `SELECT refresh_token FROM gateway_token
			WHERE provider = 'bkash' AND refresh_expires_at > now() + interval '1 hour'`).Scan(&refresh)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var t tokenResponse
		body := map[string]string{"app_key": g.cfg.AppKey, "app_secret": g.cfg.AppSecret}
		path, refreshLife := "/auth/grant-token", "now() + interval '29 days'"
		if refresh != "" {
			body["refresh_token"] = refresh
			path, refreshLife = "/auth/refresh-token", "gateway_token.refresh_expires_at"
		}
		if err := g.call(ctx, path, false, body, &t); err != nil {
			return err
		}
		tok = t.IDToken
		_, err = tx.Exec(ctx, `INSERT INTO gateway_token AS gateway_token
				(provider, access_token, refresh_token, access_expires_at, refresh_expires_at)
			VALUES ('bkash', $1, $2, now() + make_interval(secs => $3), now() + interval '29 days')
			ON CONFLICT (provider) DO UPDATE SET access_token = EXCLUDED.access_token,
				refresh_token = EXCLUDED.refresh_token, access_expires_at = EXCLUDED.access_expires_at,
				refresh_expires_at = `+refreshLife+`, updated_at = now()`,
			t.IDToken, t.RefreshToken, int(t.ExpiresIn))
		return err
	})
	return tok, err
}

// ---------- Amounts ----------

func formatAmount(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

// parseAmount reads "20", "20.5" or "20.00" into minor units.
func parseAmount(s string) (int64, error) {
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("bkash: amount %q has more than two decimals", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bkash: amount %q: %w", s, err)
	}
	f, err := strconv.ParseInt((frac + "00")[:2], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bkash: amount %q: %w", s, err)
	}
	return w*100 + f, nil
}

// ---------- Gateway ----------

// payerReference prefills a Bangladeshi wallet number on the bKash page.
func payerReference(phone, fallback string) string {
	if strings.HasPrefix(phone, "+8801") && len(phone) == 14 {
		return phone[3:]
	}
	return fallback
}

func (g *Gateway) Start(ctx context.Context, req payments.StartRequest) (payments.Started, error) {
	var out struct {
		PaymentID string `json:"paymentId"`
		URL       string `json:"bkashURL"`
	}
	err := g.call(ctx, "/payment/create", true, map[string]string{
		"mode":                  "0011",
		"payerReference":        payerReference(req.Phone, req.MerchantRef),
		"callbackURL":           req.ReturnURL,
		"amount":                formatAmount(req.AmountMinor),
		"currency":              req.Currency,
		"intent":                "sale",
		"merchantInvoiceNumber": req.MerchantRef,
	}, &out)
	if err != nil {
		return payments.Started{}, err
	}
	if out.PaymentID == "" || out.URL == "" {
		return payments.Started{}, fmt.Errorf("%w: create returned no payment", ErrUnknown)
	}
	return payments.Started{ProviderRef: out.PaymentID, RedirectURL: out.URL}, nil
}

func (g *Gateway) ParseReturn(q url.Values) (string, payments.Outcome, error) {
	ref := q.Get("paymentID")
	outcome := payments.Outcome(q.Get("status"))
	if ref == "" {
		return "", "", errors.New("bkash: callback without paymentID")
	}
	switch outcome {
	case payments.OutcomeSuccess, payments.OutcomeFailure, payments.OutcomeCancel:
		return ref, outcome, nil
	}
	return "", "", fmt.Errorf("bkash: unknown callback status %q", outcome)
}

type paymentResponse struct {
	PaymentID          string `json:"paymentId"`
	TrxID              string `json:"trxId"`
	TransactionStatus  string `json:"transactionStatus"`
	VerificationStatus string `json:"verificationStatus"`
	Amount             string `json:"amount"`
	Currency           string `json:"currency"`
	InvoiceNumber      string `json:"merchantInvoiceNumber"`
	Invoice            string `json:"merchantInvoice"`
}

func (p paymentResponse) result(ref string) (payments.Result, error) {
	res := payments.Result{ProviderRef: ref, TrxID: p.TrxID, Currency: p.Currency, MerchantRef: p.InvoiceNumber + p.Invoice}
	switch {
	case p.TransactionStatus == "Completed":
		res.Status = payments.Succeeded
		amount, err := parseAmount(p.Amount)
		if err != nil {
			return payments.Result{}, fmt.Errorf("%w: %v", ErrUnknown, err)
		}
		res.AmountMinor = amount
	case p.TransactionStatus == "Initiated" && p.VerificationStatus == "Complete":
		res.Status = payments.Authorized
	case p.TransactionStatus == "Initiated":
		res.Status = payments.Pending
	default:
		// Anything else is not a capture.
		res.Status = payments.Failed
	}
	return res, nil
}

// Complete calls Execute. A paymentId cannot be executed again after any
// Execute attempt, so whenever Execute does not return Completed itself the
// answer comes from Query, and anything but Completed is final failure.
func (g *Gateway) Complete(ctx context.Context, ref string) (payments.Result, error) {
	var out paymentResponse
	execErr := g.call(ctx, "/payment/execute", true, map[string]string{"paymentId": ref}, &out)
	if execErr == nil && out.TransactionStatus == "Completed" {
		return out.result(ref)
	}
	res, err := g.Query(ctx, ref)
	if err != nil {
		return payments.Result{}, err
	}
	if res.Status != payments.Succeeded {
		res.Status = payments.Failed
	}
	return res, nil
}

func (g *Gateway) Query(ctx context.Context, ref string) (payments.Result, error) {
	var out paymentResponse
	err := g.call(ctx, "/query/payment", true, map[string]string{"paymentId": ref}, &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == "2002" {
		// Invalid Payment ID: bKash has no such payment, so nothing was captured.
		return payments.Result{Status: payments.Failed, ProviderRef: ref}, nil
	}
	if err != nil {
		return payments.Result{}, err
	}
	return out.result(ref)
}

// retryable are refund refusals that may succeed later.
var retryable = map[string]bool{"503": true, "2003": true, "2020": true, "2023": true}

var reasons = map[string]string{
	"late_payment":     "Payment completed after the ticket hold expired",
	"duplicate_charge": "Duplicate payment for an order that was already paid",
}

// Refund first asks Refund Status, so a retry after a lost response never
// refunds twice, then calls Refund.
func (g *Gateway) Refund(ctx context.Context, req payments.RefundRequest) (payments.RefundResult, error) {
	var status struct {
		Refunds []struct {
			ID     string `json:"refundTrxId"`
			Status string `json:"refundTransactionStatus"`
			Amount string `json:"refundAmount"`
		} `json:"refundTransactions"`
	}
	err := g.call(ctx, "/refund/payment/status", true, map[string]string{"paymentId": req.ProviderRef, "trxId": req.TrxID}, &status)
	if errors.Is(err, ErrUnknown) {
		return payments.RefundResult{}, err
	}
	for _, r := range status.Refunds {
		if amount, _ := parseAmount(r.Amount); r.Status == "Completed" && amount == req.AmountMinor {
			return payments.RefundResult{Status: payments.RefundCompleted, ProviderRefundID: r.ID}, nil
		}
	}

	var out struct {
		ID     string `json:"refundTrxId"`
		Status string `json:"refundTransactionStatus"`
	}
	reason := reasons[req.Reason]
	if reason == "" {
		reason = "Ticket order refund"
	}
	err = g.call(ctx, "/refund/payment/transaction", true, map[string]string{
		"paymentId":    req.ProviderRef,
		"trxId":        req.TrxID,
		"refundAmount": formatAmount(req.AmountMinor),
		"sku":          "ticket",
		"reason":       reason,
	}, &out)
	var apiErr *APIError
	switch {
	case errors.As(err, &apiErr) && retryable[apiErr.Code]:
		return payments.RefundResult{Status: payments.RefundPending, Detail: apiErr.Error()}, nil
	case errors.As(err, &apiErr):
		return payments.RefundResult{Status: payments.RefundFailed, Detail: apiErr.Error()}, nil
	case err != nil:
		return payments.RefundResult{}, err
	case out.Status != "Completed":
		return payments.RefundResult{Status: payments.RefundPending, Detail: "refund status " + out.Status}, nil
	}
	return payments.RefundResult{Status: payments.RefundCompleted, ProviderRefundID: out.ID}, nil
}

// HTTPClient returns a client for bKash with a timeout above bKash's own.
func HTTPClient() *http.Client { return &http.Client{Timeout: 35 * time.Second} }
