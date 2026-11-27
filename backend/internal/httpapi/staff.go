package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/alexedwards/scs/v2"

	"kacheem/backend/internal/admissions"
	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/fulfil"
	"kacheem/backend/internal/ops"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/publish"
	"kacheem/backend/internal/session"
	"kacheem/backend/internal/staffauth"
	"kacheem/backend/internal/ticketops"
	"kacheem/backend/internal/tickets"
)

// StaffDeps are the collaborators of the staff API (VPN-only listener).
type StaffDeps struct {
	DB       dbx.DB
	Sessions *scs.SessionManager
	// Origin is the staff origin (also the gate origin in QR codes).
	Origin string
	Auth   *staffauth.Service
	Issuer *tickets.Issuer
	// PublishDir is the buyer web root that receives event.json; empty
	// disables publishing from the dashboard.
	PublishDir string
	Checks     []Check
	Logger     *slog.Logger
}

const (
	keyStaffID = "staff_id"
	keyEpoch   = "staff_epoch"
	orgFinSup  = "organizer,finance,support"
	orgSup     = "organizer,support"
	orgFin     = "organizer,finance"
	organizer  = "organizer"
	gateOrOrg  = "gate,organizer"
)

// NewStaff returns the handler for the staff listener. Every route but
// sign-in requires a current staff session with an allowed role.
func NewStaff(d StaffDeps) http.Handler {
	s := &staffAPI{d}
	mux := baseMux(d.Checks)
	get(mux, "/api/v1/session", s.csrf)
	post(mux, "/api/v1/staff/login", s.protect(s.login))
	post(mux, "/api/v1/staff/invite/accept", s.protect(s.acceptInvite))
	post(mux, "/api/v1/staff/logout", s.protect(s.logout))
	get(mux, "/api/v1/staff/me", s.as("organizer,finance,support,gate", s.me))

	get(mux, "/api/v1/ops/events/{slug}/my-gates", s.as(gateOrOrg, s.myGates))
	post(mux, "/api/v1/admissions", s.protect(s.as(gateOrOrg, s.admit)))

	get(mux, "/api/v1/ops/events/{slug}/summary", s.as(orgFinSup, s.summary))
	get(mux, "/api/v1/ops/orders", s.as(orgFinSup, s.searchOrders))
	get(mux, "/api/v1/ops/orders/{ref}", s.as(orgFinSup, s.orderDetail))
	post(mux, "/api/v1/ops/orders/{ref}/resend", s.protect(s.as(orgSup, s.resend)))
	post(mux, "/api/v1/ops/orders/{ref}/refund", s.protect(s.as(orgFin, s.refund)))
	post(mux, "/api/v1/ops/tickets/{id}/reissue", s.protect(s.as(orgSup, s.reissue)))
	post(mux, "/api/v1/ops/tickets/{id}/void", s.protect(s.as(orgSup, s.void)))
	get(mux, "/api/v1/ops/finance", s.as(orgFin, s.finance))

	methods(mux, "/api/v1/ops/events/{slug}", s.as(organizer, s.event), s.protect(s.as(organizer, s.updateEvent)))
	post(mux, "/api/v1/ops/events/{slug}/capacity", s.protect(s.as(organizer, s.capacity)))
	post(mux, "/api/v1/ops/events/{slug}/publish", s.protect(s.as(organizer, s.publish)))
	methods(mux, "/api/v1/ops/events/{slug}/gates", s.as(organizer, s.gates), s.protect(s.as(organizer, s.addGate)))
	post(mux, "/api/v1/ops/gates/{id}/assign", s.protect(s.as(organizer, s.assign)))
	get(mux, "/api/v1/ops/staff", s.as(organizer, s.staffList))
	post(mux, "/api/v1/ops/staff/invite", s.protect(s.as(organizer, s.invite)))
	post(mux, "/api/v1/ops/staff/{id}/disable", s.protect(s.as(organizer, s.disable)))
	get(mux, "/api/v1/ops/audit", s.as(organizer, s.auditLog))
	return withDefaults(d.Sessions.LoadAndSave(mux))
}

type staffAPI struct{ StaffDeps }

type staffHandler func(w http.ResponseWriter, r *http.Request, st staffauth.Staff)

func (s *staffAPI) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Logger.Error("staff request failed", "err", err, "method", r.Method, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Please try again.")
}

func (s *staffAPI) protect(next http.HandlerFunc) http.HandlerFunc {
	return (&public{PublicDeps{Sessions: s.Sessions, Origin: s.Origin}}).protect(next)
}

// as admits only a current staff session whose role is listed.
func (s *staffAPI) as(roles string, next staffHandler) http.HandlerFunc {
	allowed := bytes.Split([]byte(roles), []byte(","))
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		st, err := s.Auth.Current(ctx, s.Sessions.GetString(ctx, keyStaffID), s.Sessions.GetInt(ctx, keyEpoch))
		if errors.Is(err, staffauth.ErrRevoked) {
			s.Sessions.Remove(ctx, keyStaffID)
			writeError(w, http.StatusUnauthorized, "signed_out", "Please sign in.")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if !slices.ContainsFunc(allowed, func(b []byte) bool { return string(b) == st.Role }) {
			writeError(w, http.StatusForbidden, "forbidden", "Your role cannot do this.")
			return
		}
		next(w, r, st)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "The request body is not valid.")
		return false
	}
	return true
}

func (s *staffAPI) csrf(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": session.Ensure(s.Sessions, r).CSRFToken})
}

func staffJSON(st staffauth.Staff) map[string]string {
	return map[string]string{"id": st.ID, "email": st.Email, "name": st.Name, "role": st.Role, "status": st.Status}
}

// ---------- Sign-in ----------

func (s *staffAPI) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decode(w, r, &body) {
		st, err := s.Auth.SignIn(r.Context(), body.Email, body.Password)
		s.signIn(w, r, st, err)
	}
}

func (s *staffAPI) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if decode(w, r, &body) {
		st, err := s.Auth.AcceptInvite(r.Context(), body.Token, body.Password)
		s.signIn(w, r, st, err)
	}
}

// signIn starts an authenticated session on a fresh token.
func (s *staffAPI) signIn(w http.ResponseWriter, r *http.Request, st staffauth.Staff, err error) {
	ctx := r.Context()
	if err == nil {
		err = s.Sessions.RenewToken(ctx)
	}
	switch {
	case errors.Is(err, staffauth.ErrLogin):
		writeError(w, http.StatusUnauthorized, "sign_in_failed", "Those details did not match. Check them and try again.")
	case errors.Is(err, staffauth.ErrInvite):
		writeError(w, http.StatusNotFound, "invite_invalid", "This invitation is not valid or has expired.")
	case errors.Is(err, staffauth.ErrPassword):
		writeError(w, http.StatusUnprocessableEntity, "weak_password", err.Error())
	case err != nil:
		s.internal(w, r, err)
	default:
		s.Sessions.Put(ctx, keyStaffID, st.ID)
		s.Sessions.Put(ctx, keyEpoch, st.Epoch)
		writeJSON(w, http.StatusOK, map[string]any{"staff": staffJSON(st)})
	}
}

func (s *staffAPI) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Sessions.Destroy(r.Context()); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed_out"})
}

func (s *staffAPI) me(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	events, err := dbx.All[struct {
		Slug  string `db:"slug" json:"slug"`
		Title string `db:"title" json:"title"`
	}](r.Context(), s.DB, `SELECT slug, title FROM event ORDER BY starts_at`)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": staffJSON(st), "events": events})
}

// ---------- Gate ----------

func (s *staffAPI) eventID(w http.ResponseWriter, r *http.Request) (catalog.Event, bool) {
	e, err := catalog.GetEvent(r.Context(), s.DB, r.PathValue("slug"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event_not_found", "No such event.")
		return e, false
	}
	if err != nil {
		s.internal(w, r, err)
		return e, false
	}
	return e, true
}

func (s *staffAPI) myGates(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	e, ok := s.eventID(w, r)
	if !ok {
		return
	}
	gates, err := ops.GatesFor(r.Context(), s.DB, st.ID, st.Role, e.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gates": gates})
}

var resultMessages = map[string]string{
	admissions.Admitted:    "Admit",
	admissions.AlreadyUsed: "Already used",
	admissions.Unknown:     "Do not admit: unknown ticket",
	admissions.WrongEvent:  "Do not admit: ticket for another event",
	admissions.Void:        "Do not admit: ticket cancelled or refunded",
	admissions.NotOpen:     "Do not admit: doors are not open yet",
	admissions.Closed:      "Do not admit: admission has closed",
}

// admit decides one scan. Gate staff may scan only at gates assigned to them.
func (s *staffAPI) admit(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body struct {
		GateID    string `json:"gate_id"`
		DeviceID  string `json:"device_id"`
		RequestID string `json:"request_id"`
		QR        string `json:"qr"`
		Code      string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	var allowed bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM gate g WHERE g.id::text = $1 AND ($3 = 'organizer'
			OR EXISTS (SELECT 1 FROM gate_assignment ga WHERE ga.gate_id = g.id AND ga.staff_id::text = $2)))`,
		body.GateID, st.ID, st.Role).Scan(&allowed); err != nil {
		s.internal(w, r, err)
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "gate_not_assigned", "You are not assigned to this gate.")
		return
	}
	res, err := admissions.Admit(ctx, s.DB, s.Origin, admissions.Scan{DeviceID: body.DeviceID, RequestID: body.RequestID,
		GateID: body.GateID, StaffID: st.ID, QR: body.QR, Code: body.Code})
	if errors.Is(err, admissions.ErrBadScan) {
		writeError(w, http.StatusUnprocessableEntity, "bad_scan", "That is not a ticket code. Check it and try again.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := map[string]any{"outcome": res.Outcome, "message": resultMessages[res.Outcome], "replay": res.Replay,
		"ticket": res.TicketLabel, "order_ref": res.OrderRef, "decided_at": res.DecidedAt}
	if res.Outcome == admissions.AlreadyUsed || (res.Replay && !res.FirstAt.IsZero()) {
		out["first_gate"], out["first_at"] = res.FirstGate, res.FirstAt
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- Dashboard ----------

func (s *staffAPI) summary(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	sum, err := ops.GetSummary(r.Context(), s.DB, r.PathValue("slug"))
	if errors.Is(err, ops.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event_not_found", "No such event.")
		return
	}
	var counts []admissions.Counts
	if err == nil {
		counts, err = admissions.GateCounts(r.Context(), s.DB, sum.EventID)
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": sum, "gates": counts})
}

func unmasked(role string) bool { return role == "organizer" || role == "finance" }

func (s *staffAPI) searchOrders(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	rows, err := ops.Search(r.Context(), s.DB, r.URL.Query().Get("q"), unmasked(st.Role))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": rows})
}

func (s *staffAPI) orderDetail(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	d, err := ops.GetDetail(r.Context(), s.DB, r.PathValue("ref"), unmasked(st.Role))
	if errors.Is(err, ops.ErrNotFound) {
		writeError(w, http.StatusNotFound, "order_not_found", "No such order.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": d.Order, "tickets": d.Tickets, "attempts": d.Attempts,
		"refunds": d.Refunds, "history": d.History})
}

// orderID resolves a reference for the actions on an order.
func (s *staffAPI) orderID(w http.ResponseWriter, r *http.Request) (string, bool) {
	d, err := ops.GetDetail(r.Context(), s.DB, r.PathValue("ref"), false)
	if errors.Is(err, ops.ErrNotFound) {
		writeError(w, http.StatusNotFound, "order_not_found", "No such order.")
		return "", false
	}
	if err != nil {
		s.internal(w, r, err)
		return "", false
	}
	return d.ID, true
}

func (s *staffAPI) opError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "done"})
	case errors.Is(err, ticketops.ErrReason):
		writeError(w, http.StatusUnprocessableEntity, "reason_required", "Give a reason; it is kept in the audit log.")
	case errors.Is(err, ticketops.ErrAdmitted):
		writeError(w, http.StatusConflict, "admitted", "A ticket was already used at the gate. A supervisor must review this.")
	case errors.Is(err, ticketops.ErrNotValid):
		writeError(w, http.StatusConflict, "not_valid", "This ticket is already void or blocked by a refund.")
	case errors.Is(err, ticketops.ErrNoPayment):
		writeError(w, http.StatusConflict, "no_payment", "There is no captured payment left to refund.")
	case errors.Is(err, ticketops.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such ticket or order.")
	case errors.Is(err, fulfil.ErrTooSoon):
		writeError(w, http.StatusTooManyRequests, "too_soon", "The tickets were sent a few minutes ago.")
	default:
		s.internal(w, r, err)
	}
}

type reasonBody struct {
	Reason string `json:"reason"`
}

func (s *staffAPI) resend(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	if id, ok := s.orderID(w, r); ok {
		err := fulfil.Resend(r.Context(), s.DB, id)
		if err == nil {
			err = audit.Record(r.Context(), s.DB, audit.Staff(st.ID), "order.resend", "order", id, "", nil)
		}
		s.opError(w, r, err)
	}
}

func (s *staffAPI) refund(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body reasonBody
	if !decode(w, r, &body) {
		return
	}
	if id, ok := s.orderID(w, r); ok {
		s.opError(w, r, ticketops.RequestRefund(r.Context(), s.DB, st.ID, id, body.Reason))
	}
}

func (s *staffAPI) reissue(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body reasonBody
	if decode(w, r, &body) {
		s.opError(w, r, ticketops.Reissue(r.Context(), s.DB, s.Issuer, st.ID, r.PathValue("id"), body.Reason))
	}
}

func (s *staffAPI) void(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body reasonBody
	if decode(w, r, &body) {
		s.opError(w, r, ticketops.Void(r.Context(), s.DB, st.ID, r.PathValue("id"), body.Reason))
	}
}

func (s *staffAPI) finance(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	l, err := payments.GetLedger(r.Context(), s.DB)
	var queue []ops.FinanceItem
	if err == nil {
		queue, err = ops.FinanceQueue(r.Context(), s.DB)
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ledger": l, "balanced": l.Balanced(), "queue": queue})
}

// ---------- Organizer ----------

func (s *staffAPI) event(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	if e, ok := s.eventID(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"event": e, "tiers": e.Tiers})
	}
}

func (s *staffAPI) updateEvent(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var d catalog.Details
	if !decode(w, r, &d) {
		return
	}
	if e, ok := s.eventID(w, r); ok {
		err := catalog.Update(r.Context(), s.DB, audit.Staff(st.ID), e.ID, d)
		if errors.Is(err, catalog.ErrInvalid) {
			writeError(w, http.StatusUnprocessableEntity, "invalid", "Some details are not valid. Check the dates, limits and time zone.")
			return
		}
		s.opError(w, r, err)
	}
}

func (s *staffAPI) capacity(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body struct {
		Capacity int `json:"capacity"`
	}
	if !decode(w, r, &body) {
		return
	}
	e, ok := s.eventID(w, r)
	if !ok {
		return
	}
	err := catalog.SetCapacity(r.Context(), s.DB, audit.Staff(st.ID), e.ID, body.Capacity)
	var below *catalog.BelowCommittedError
	if errors.As(err, &below) {
		writeErrorWith(w, http.StatusConflict, "below_committed", below.Error(), map[string]any{"min": below.Min})
		return
	}
	s.opError(w, r, err)
}

func (s *staffAPI) publish(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	if s.PublishDir == "" {
		writeError(w, http.StatusServiceUnavailable, "publish_unavailable", "Publishing is not configured on this server.")
		return
	}
	snap, err := publish.Publish(r.Context(), s.DB, audit.Staff(st.ID), r.PathValue("slug"), s.PublishDir)
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "event_not_found", "No such event.")
	case errors.Is(err, publish.ErrInvalidEvent):
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"revision": snap.Revision, "published_at": snap.PublishedAt})
	}
}

func (s *staffAPI) gates(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	e, ok := s.eventID(w, r)
	if !ok {
		return
	}
	gates, err := ops.Gates(r.Context(), s.DB, e.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gates": gates})
}

func (s *staffAPI) addGate(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	e, ok := s.eventID(w, r)
	if !ok {
		return
	}
	id, err := ops.AddGate(r.Context(), s.DB, audit.Staff(st.ID), e.ID, body.Name)
	if dbx.IsUniqueViolation(err, "gate_event_id_name_key") {
		writeError(w, http.StatusConflict, "exists", "A gate with that name exists.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *staffAPI) assign(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body struct {
		StaffID string `json:"staff_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	err := ops.Assign(r.Context(), s.DB, audit.Staff(st.ID), r.PathValue("id"), body.StaffID)
	if errors.Is(err, ops.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such gate or staff member.")
		return
	}
	s.opError(w, r, err)
}

func (s *staffAPI) staffList(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	list, err := s.Auth.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]map[string]string, len(list))
	for i, x := range list {
		out[i] = staffJSON(x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": out})
}

func (s *staffAPI) invite(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, err := s.Auth.Invite(r.Context(), audit.Staff(st.ID), body.Email, body.Name, body.Role)
	switch {
	case errors.Is(err, staffauth.ErrInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid", "Give a name, an email and a role.")
	case errors.Is(err, staffauth.ErrExists):
		writeError(w, http.StatusConflict, "exists", "That person already has an active account.")
	case err != nil:
		s.internal(w, r, err)
	default:
		// The link is shown once to the organizer, who sends it to the person.
		writeJSON(w, http.StatusCreated, map[string]string{"invite_url": s.Origin + "/invite/#" + token})
	}
}

func (s *staffAPI) disable(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	var body reasonBody
	if !decode(w, r, &body) {
		return
	}
	if body.Reason == "" {
		s.opError(w, r, ticketops.ErrReason)
		return
	}
	err := s.Auth.Disable(r.Context(), audit.Staff(st.ID), r.PathValue("id"), body.Reason)
	if errors.Is(err, staffauth.ErrInput) {
		writeError(w, http.StatusNotFound, "not_found", "No such staff member.")
		return
	}
	s.opError(w, r, err)
}

func (s *staffAPI) auditLog(w http.ResponseWriter, r *http.Request, st staffauth.Staff) {
	entries, err := audit.Recent(r.Context(), s.DB, 200)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
