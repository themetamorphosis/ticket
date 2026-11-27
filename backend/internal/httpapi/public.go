package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/alexedwards/scs/v2"

	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/session"
)

// PublicDeps are the collaborators of the buyer-facing API.
type PublicDeps struct {
	DB       dbx.DB
	Sessions *scs.SessionManager
	// Origin is the exact buyer origin browser writes must come from,
	// for example "https://tickets.example.org".
	Origin string
	Checks []Check
	Logger *slog.Logger
	// Payments runs checkout and settlement for the configured gateway.
	Payments *payments.Service
	// FakeGatewayPage serves the fake gateway's hosted page in development;
	// nil with a real provider.
	FakeGatewayPage http.Handler
}

// NewPublic returns the handler for the buyer-facing listener. Staff routes
// are not registered here, so they cannot be reached through this listener.
func NewPublic(d PublicDeps) http.Handler {
	p := &public{d}
	mux := baseMux(d.Checks)
	get(mux, "/api/v1/session", p.session)
	get(mux, "/api/v1/events/{slug}/availability", p.availability)
	get(mux, "/api/v1/events/{slug}/quote", p.quote)
	post(mux, "/api/v1/orders", p.protect(p.createOrder))
	get(mux, "/api/v1/orders/{ref}", p.getOrder)
	post(mux, "/api/v1/orders/{ref}/cancel", p.protect(p.cancelOrder))
	post(mux, "/api/v1/orders/{ref}/checkout", p.protect(p.checkout))
	get(mux, "/payments/return/{provider}", p.paymentReturn)
	if d.FakeGatewayPage != nil {
		mux.Handle("/payments/fake/pay/{ref}", d.FakeGatewayPage)
	}
	return withDefaults(d.Sessions.LoadAndSave(mux))
}

type public struct{ PublicDeps }

// SessionError answers a session-store failure; pass it to session.NewBuyer.
func SessionError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("buyer session store", "err", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Please try again.")
	}
}

func (p *public) internal(w http.ResponseWriter, r *http.Request, err error) {
	p.Logger.Error("request failed", "err", err, "method", r.Method, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Please try again.")
}

// protect refuses browser writes from another origin or without this
// session's CSRF token.
func (p *public) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch session.CheckWrite(p.Sessions, r, p.Origin) {
		case "bad_origin":
			writeError(w, http.StatusForbidden, "bad_origin", "This request did not come from the ticket site.")
		case "bad_csrf":
			writeError(w, http.StatusForbidden, "bad_csrf", "Your session expired. Reload the page and try again.")
		default:
			next(w, r)
		}
	}
}

// session creates (or returns) the anonymous buyer session and its CSRF token.
func (p *public) session(w http.ResponseWriter, r *http.Request) {
	b := session.Ensure(p.Sessions, r)
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": b.CSRFToken})
}

func (p *public) availability(w http.ResponseWriter, r *http.Request) {
	a, err := catalog.GetAvailability(r.Context(), p.DB, r.PathValue("slug"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event_not_found", "No such event.")
		return
	}
	if err != nil {
		p.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state":       a.State,
		"remaining":   a.Remaining,
		"server_time": a.ServerTime.UTC().Format(time.RFC3339Nano),
		"opens_at":    a.OpensAt.UTC().Format(time.RFC3339),
		"closes_at":   a.ClosesAt.UTC().Format(time.RFC3339),
	})
}

func (p *public) quote(w http.ResponseWriter, r *http.Request) {
	e, err := catalog.GetPublishedEvent(r.Context(), p.DB, r.PathValue("slug"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event_not_found", "No such event.")
		return
	}
	if err != nil {
		p.internal(w, r, err)
		return
	}
	tier, err := e.Tier()
	if err != nil {
		p.internal(w, r, err)
		return
	}
	qty, err := strconv.Atoi(r.URL.Query().Get("quantity"))
	if err != nil || qty < 1 || qty > e.MaxPerOrder {
		writeErrorWith(w, http.StatusUnprocessableEntity, "invalid", "Choose a valid number of tickets.",
			map[string]any{"fields": map[string]string{"quantity": "Choose between 1 and " + strconv.Itoa(e.MaxPerOrder) + " tickets."}})
		return
	}
	writeJSON(w, http.StatusOK, quoteJSON(catalog.NewQuote(tier, e.Currency, qty)))
}

func quoteJSON(q catalog.Quote) map[string]any {
	return map[string]any{
		"quantity":         q.Quantity,
		"unit_price_minor": q.UnitPriceMinor,
		"unit_fee_minor":   q.UnitFeeMinor,
		"subtotal_minor":   q.SubtotalMinor,
		"fees_minor":       q.FeesMinor,
		"total_minor":      q.TotalMinor,
		"currency":         q.Currency,
	}
}

var idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)

type orderBody struct {
	Event    string `json:"event"`
	Quantity int    `json:"quantity"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Age      int    `json:"age"`
}

func (p *public) createOrder(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if !idempotencyKey.MatchString(key) {
		writeError(w, http.StatusBadRequest, "bad_idempotency_key", "Missing or malformed Idempotency-Key header.")
		return
	}
	var body orderBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, "bad_json", "The request body is not a valid booking.")
		return
	}
	o, created, err := orders.Reserve(r.Context(), p.DB, orders.Request{
		EventSlug:      body.Event,
		BuyerID:        session.Current(p.Sessions, r).ID,
		IdempotencyKey: key,
		Input:          orders.Input{Quantity: body.Quantity, Name: body.Name, Email: body.Email, Phone: body.Phone, Age: body.Age},
	})
	if err != nil {
		p.orderError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, orderJSON(o))
}

func (p *public) orderError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		fields    orders.FieldErrors
		notOnSale *orders.NotOnSaleError
		soldOut   *orders.SoldOutError
		active    *orders.ActiveHoldError
	)
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "event_not_found", "No such event.")
	case errors.As(err, &fields):
		writeErrorWith(w, http.StatusUnprocessableEntity, "invalid", "Check the highlighted details.",
			map[string]any{"fields": fields})
	case errors.Is(err, orders.ErrIdempotencyConflict):
		writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused",
			"These details changed since the first attempt. Review the new total and submit again.")
	case errors.As(err, &notOnSale):
		writeErrorWith(w, http.StatusConflict, "not_on_sale", "Tickets are not on sale right now.",
			map[string]any{"state": notOnSale.State})
	case errors.As(err, &soldOut) && soldOut.Remaining == 0:
		writeErrorWith(w, http.StatusConflict, "sold_out", "No places are free right now.",
			map[string]any{"remaining": 0})
	case errors.As(err, &soldOut):
		writeErrorWith(w, http.StatusConflict, "insufficient",
			"Only "+strconv.Itoa(soldOut.Remaining)+" places are free. Choose fewer tickets.",
			map[string]any{"remaining": soldOut.Remaining})
	case errors.As(err, &active):
		writeErrorWith(w, http.StatusConflict, "active_hold", "You already have tickets on hold.",
			map[string]any{"order_ref": active.Ref})
	case errors.Is(err, orders.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, "order_not_found", "No such order.")
	case errors.Is(err, orders.ErrNotHeld):
		writeError(w, http.StatusConflict, "not_held", "This order is no longer on hold.")
	default:
		p.internal(w, r, err)
	}
}

func (p *public) getOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := orders.Get(ctx, p.DB, session.Current(p.Sessions, r).ID, r.PathValue("ref"))
	if err != nil {
		p.orderError(w, r, err)
		return
	}
	status, err := payments.LatestStatus(ctx, p.DB, o.ID)
	if err != nil {
		p.internal(w, r, err)
		return
	}
	timeline, err := payments.Timeline(ctx, p.DB, o.ID)
	if err != nil {
		p.internal(w, r, err)
		return
	}
	body := orderJSON(o)
	body["payment_provider"] = p.Payments.Gateway.Provider()
	body["payment_status"] = nil
	if status != "" {
		body["payment_status"] = status
	}
	entries := make([]map[string]string, len(timeline))
	for i, e := range timeline {
		entries[i] = map[string]string{"at": e.At.UTC().Format(time.RFC3339), "kind": e.Kind}
	}
	body["timeline"] = entries
	writeJSON(w, http.StatusOK, body)
}

func (p *public) checkout(w http.ResponseWriter, r *http.Request) {
	redirect, err := p.Payments.Checkout(r.Context(), session.Current(p.Sessions, r).ID, r.PathValue("ref"))
	switch {
	case errors.Is(err, payments.ErrHoldEnded):
		writeError(w, http.StatusConflict, "hold_ended", "This order is no longer on hold, or too little time is left to pay.")
	case errors.Is(err, payments.ErrGatewayUnavailable):
		p.Logger.Warn("payment start failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "payment_unavailable",
			"Payment is unavailable right now. Your tickets are still held; please try again in a moment.")
	case err != nil:
		p.orderError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"redirect_url": redirect})
	}
}

// paymentReturn is where the provider sends the buyer back. It settles the
// payment with the provider and redirects to the order page, which shows the
// confirmed result. Unknown or unreadable returns go back to the event page.
func (p *public) paymentReturn(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("provider") != p.Payments.Gateway.Provider() {
		writeError(w, http.StatusNotFound, "not_found", "No such API route.")
		return
	}
	ref, err := p.Payments.Return(r.Context(), r.URL.Query())
	if err != nil {
		p.Logger.Warn("payment return", "err", err, "order_ref", ref)
	}
	target := "/"
	if ref != "" {
		target = "/order/?ref=" + url.QueryEscape(ref)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (p *public) cancelOrder(w http.ResponseWriter, r *http.Request) {
	buyer := session.Current(p.Sessions, r).ID
	ref := r.PathValue("ref")
	if err := orders.Cancel(r.Context(), p.DB, buyer, ref); err != nil {
		p.orderError(w, r, err)
		return
	}
	p.getOrder(w, r)
}

func orderJSON(o orders.Order) map[string]any {
	return map[string]any{
		"ref":    o.Ref,
		"status": o.Status,
		"event": map[string]any{
			"slug":      o.EventSlug,
			"title":     o.EventTitle,
			"starts_at": o.EventStartsAt.UTC().Format(time.RFC3339),
			"time_zone": o.EventTimeZone,
		},
		"quantity":         o.Quantity,
		"unit_price_minor": o.UnitPriceMinor,
		"unit_fee_minor":   o.UnitFeeMinor,
		"total_minor":      o.TotalMinor,
		"currency":         o.Currency,
		"purchaser":        map[string]any{"name": o.Name, "email": o.Email, "phone": o.Phone, "age": o.Age},
		"created_at":       o.CreatedAt.UTC().Format(time.RFC3339),
		"expires_at":       o.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"server_time":      o.ServerTime.UTC().Format(time.RFC3339Nano),
	}
}
