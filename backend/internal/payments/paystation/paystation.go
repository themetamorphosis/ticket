// Package paystation implements payments.Gateway for PayStation hosted
// checkout (https://www.paystation.com.bd/documentation), version 1 API.
//
// PayStation captures the money inside its own page; there is no merchant
// capture call, so Complete only verifies. PayStation has no session timeout
// setting, so a buyer can still pay after the hold ends: the payments service
// then records the capture and queues a refund. PayStation has no refund API
// either: a refund is made by finance in the PayStation merchant panel, and
// Refund reports it completed once PayStation shows the transaction as
// refunded.
package paystation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kacheem/backend/internal/payments"
)

// Documented base URLs.
const (
	SandboxURL = "https://sandbox.paystation.com.bd"
	LiveURL    = "https://api.paystation.com.bd"
)

// Config holds the merchant credentials from the runtime environment.
type Config struct {
	BaseURL    string
	MerchantID string
	Password   string
}

// Gateway talks to PayStation.
type Gateway struct {
	cfg  Config
	http *http.Client
}

// New returns a PayStation gateway.
func New(cfg Config, client *http.Client) *Gateway { return &Gateway{cfg: cfg, http: client} }

// HTTPClient returns a client with a bounded timeout for PayStation calls.
func HTTPClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// ErrUnknown marks an outcome PayStation did not report (timeout, network,
// server error, unreadable answer): ask again later.
var ErrUnknown = errors.New("paystation: outcome unknown")

// APIError is a definite error answer, such as 1008 duplicate invoice.
type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string { return "paystation: " + e.Code + " " + e.Message }

func (g *Gateway) Provider() string         { return "paystation" }
func (g *Gateway) CapturesOnComplete() bool { return false }

type envelope struct {
	StatusCode string `json:"status_code"`
	Message    string `json:"message"`
}

// call posts a form and decodes a response whose status_code is "200".
func (g *Gateway) call(ctx context.Context, path string, form url.Values, header map[string]string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.BaseURL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		var uerr *url.Error
		errors.As(err, &uerr)
		return fmt.Errorf("%w: %s: %v", ErrUnknown, path, uerr.Err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode >= 500 {
		return fmt.Errorf("%w: %s: HTTP %d", ErrUnknown, path, resp.StatusCode)
	}
	var env envelope
	if json.Unmarshal(data, &env) != nil || json.Unmarshal(data, out) != nil {
		return fmt.Errorf("%w: %s: unreadable response", ErrUnknown, path)
	}
	if env.StatusCode != "200" {
		return &APIError{Code: env.StatusCode, Message: env.Message}
	}
	return nil
}

// amount renders minor units as PayStation's payment_amount: whole taka as
// an integer (the documented type), with two decimals only when needed.
func amount(minor int64) string {
	if minor%100 == 0 {
		return fmt.Sprint(minor / 100)
	}
	return payments.FormatAmount(minor)
}

// localPhone turns +8801XXXXXXXXX into the documented 01XXXXXXXXX form.
func localPhone(phone string) string {
	if strings.HasPrefix(phone, "+880") {
		return phone[3:]
	}
	return phone
}

// Start initiates a payment. Our merchant reference is the invoice number,
// which is also PayStation's handle for status queries. The merchant pays the
// gateway charge (pay_with_charge=0), so the buyer pays exactly the quote.
func (g *Gateway) Start(ctx context.Context, req payments.StartRequest) (payments.Started, error) {
	var out struct {
		InvoiceNumber string `json:"invoice_number"`
		PaymentURL    string `json:"payment_url"`
	}
	err := g.call(ctx, "/initiate-payment", url.Values{
		"merchantId":      {g.cfg.MerchantID},
		"password":        {g.cfg.Password},
		"invoice_number":  {req.MerchantRef},
		"currency":        {req.Currency},
		"payment_amount":  {amount(req.AmountMinor)},
		"pay_with_charge": {"0"},
		"reference":       {"Event tickets"},
		"cust_name":       {req.Name},
		"cust_phone":      {localPhone(req.Phone)},
		"cust_email":      {req.Email},
		"callback_url":    {req.ReturnURL},
	}, nil, &out)
	if err != nil {
		return payments.Started{}, err
	}
	if out.PaymentURL == "" {
		return payments.Started{}, fmt.Errorf("%w: initiate-payment returned no payment_url", ErrUnknown)
	}
	return payments.Started{ProviderRef: req.MerchantRef, RedirectURL: out.PaymentURL}, nil
}

// ParseReturn reads the callback: status, invoice_number and trx_id (as the
// official WooCommerce plugin reads them).
func (g *Gateway) ParseReturn(q url.Values) (string, payments.Outcome, error) {
	ref, status := q.Get("invoice_number"), strings.ToLower(q.Get("status"))
	switch {
	case ref == "" || status == "":
		return "", "", errors.New("paystation: callback without status or invoice_number")
	case status == "successful" || status == "success":
		return ref, payments.OutcomeSuccess, nil
	case status == "canceled" || status == "cancelled":
		return ref, payments.OutcomeCancel, nil
	}
	return ref, payments.OutcomeFailure, nil
}

// Complete verifies: PayStation has already captured inside its page.
func (g *Gateway) Complete(ctx context.Context, ref string) (payments.Result, error) {
	return g.Query(ctx, ref)
}

type status struct {
	Data struct {
		InvoiceNumber string `json:"invoice_number"`
		TrxStatus     string `json:"trx_status"`
		TrxID         string `json:"trx_id"`
		PaymentAmount string `json:"payment_amount"`
	} `json:"data"`
}

func (g *Gateway) status(ctx context.Context, ref string) (status, error) {
	var out status
	err := g.call(ctx, "/transaction-status", url.Values{"invoice_number": {ref}},
		map[string]string{"merchantId": g.cfg.MerchantID}, &out)
	return out, err
}

// Query asks Transaction Status v1 by invoice number.
func (g *Gateway) Query(ctx context.Context, ref string) (payments.Result, error) {
	out, err := g.status(ctx, ref)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == "2001" {
		// Not found (yet): keep the payment open rather than risk ignoring a
		// capture; reconciliation closes it once the reference is too old.
		return payments.Result{Status: payments.Pending, ProviderRef: ref}, nil
	}
	if err != nil {
		return payments.Result{}, err
	}
	d := out.Data
	res := payments.Result{ProviderRef: ref, TrxID: d.TrxID, Currency: "BDT", MerchantRef: d.InvoiceNumber}
	switch strings.ToLower(d.TrxStatus) {
	case "success", "successful":
		res.Status = payments.Succeeded
		amount, err := payments.ParseAmount(d.PaymentAmount)
		if err != nil {
			return payments.Result{}, fmt.Errorf("%w: %v", ErrUnknown, err)
		}
		res.AmountMinor = amount
	case "failed", "canceled", "cancelled", "refund", "refunded":
		// A refunded payment netted to nothing for this attempt.
		res.Status = payments.Failed
	default:
		// "processing" and anything new: not captured yet.
		res.Status = payments.Pending
	}
	return res, nil
}

// Refund cannot call PayStation (it has no refund API). It reports the
// refund completed once PayStation shows the transaction refunded, and
// otherwise pending, so the refund stays visible to finance until done.
func (g *Gateway) Refund(ctx context.Context, req payments.RefundRequest) (payments.RefundResult, error) {
	out, err := g.status(ctx, req.ProviderRef)
	if err != nil {
		return payments.RefundResult{}, err
	}
	if s := strings.ToLower(out.Data.TrxStatus); s == "refund" || s == "refunded" {
		return payments.RefundResult{Status: payments.RefundCompleted, ProviderRefundID: "panel:" + out.Data.TrxID}, nil
	}
	return payments.RefundResult{Status: payments.RefundPending,
		Detail: "refund this transaction in the PayStation merchant panel; it completes here once PayStation reports it refunded"}, nil
}

// ParseNotification reads the invoice number from a PayStation IPN. Nothing
// else in the body is trusted.
func (g *Gateway) ParseNotification(body []byte) (string, error) {
	var n struct {
		InvoiceNumber string `json:"invoice_number"`
	}
	if err := json.Unmarshal(body, &n); err != nil || n.InvoiceNumber == "" {
		return "", errors.New("paystation: notification without invoice_number")
	}
	return n.InvoiceNumber, nil
}
