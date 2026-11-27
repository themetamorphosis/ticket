// Package fulfil runs the outbox jobs that turn a paid order into delivered
// tickets: render every PDF, then email them and text a private pickup link.
package fulfil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/delivery"
	"kacheem/backend/internal/outbox"
	"kacheem/backend/internal/tickets"
)

const (
	// lease bounds how long one job may run before another worker reclaims it.
	lease = 2 * time.Minute
	// attachmentsPerEmail splits large orders into numbered emails.
	attachmentsPerEmail = 10
	// grantGrace keeps pickup links working until after the event.
	grantGrace = 48 * time.Hour
	// ResendCooldown limits buyer-requested resends per order.
	ResendCooldown = 10 * time.Minute
)

// ErrNotReady means a delivery job ran before every PDF was rendered.
var ErrNotReady = errors.New("fulfil: tickets not rendered yet")

// ErrTooSoon means a resend was requested within ResendCooldown.
var ErrTooSoon = errors.New("fulfil: tickets were resent recently")

// Fulfiller runs fulfilment jobs.
type Fulfiller struct {
	DB     dbx.DB
	Issuer *tickets.Issuer
	Mailer delivery.Mailer
	SMS    delivery.SMS
	// PublicOrigin hosts the pickup page; GateOrigin hosts the staff scanner
	// named in every QR code.
	PublicOrigin string
	GateOrigin   string
}

// Run claims and runs up to limit due jobs and returns how many it ran.
func (f *Fulfiller) Run(ctx context.Context, limit int) (int, error) {
	jobs, err := outbox.Claim(ctx, f.DB, limit, lease)
	if err != nil {
		return 0, err
	}
	for i, j := range jobs {
		if err := f.handle(ctx, j); err != nil {
			if err := outbox.Retry(ctx, f.DB, j, err); err != nil {
				return i, err
			}
			continue
		}
		if err := outbox.Done(ctx, f.DB, j.ID); err != nil {
			return i, err
		}
	}
	return len(jobs), nil
}

func (f *Fulfiller) handle(ctx context.Context, j outbox.Job) error {
	switch j.Kind {
	case "render_tickets":
		return f.render(ctx, j.OrderID)
	case "deliver_email":
		return f.email(ctx, j.OrderID)
	case "deliver_sms":
		return f.sms(ctx, j.OrderID)
	}
	return fmt.Errorf("fulfil: unknown job kind %q", j.Kind)
}

type ticketRow struct {
	ID         string    `db:"id"`
	Ordinal    int       `db:"ordinal"`
	Of         int       `db:"of"`
	Sealed     []byte    `db:"credential_sealed"`
	Version    int       `db:"credential_version"`
	Rendered   bool      `db:"rendered"`
	OrderRef   string    `db:"public_ref"`
	Title      string    `db:"title"`
	Organizer  string    `db:"organizer"`
	Venue      string    `db:"venue"`
	StartsAt   time.Time `db:"starts_at"`
	TimeZone   string    `db:"time_zone"`
	TicketType string    `db:"ticket_type"`
}

func (f *Fulfiller) tickets(ctx context.Context, orderID string) ([]ticketRow, error) {
	return dbx.All[ticketRow](ctx, f.DB, `
		SELECT t.id::text, t.ordinal, count(*) OVER ()::int AS of, t.credential_sealed, t.credential_version,
			EXISTS (SELECT 1 FROM ticket_document d WHERE d.ticket_id = t.id AND d.version = t.credential_version) AS rendered,
			o.public_ref, e.title, e.organizer,
			e.venue_name || CASE WHEN e.venue_address <> '' THEN ', ' || e.venue_address ELSE '' END AS venue,
			e.starts_at, e.time_zone, tt.name AS ticket_type
		FROM ticket t
		JOIN ticket_order o ON o.id = t.order_id
		JOIN event e ON e.id = o.event_id
		JOIN ticket_type tt ON tt.id = t.ticket_type_id
		WHERE t.order_id = $1 AND t.status <> 'void' ORDER BY t.ordinal`, orderID)
}

// render renders each ticket without a current PDF, then queues delivery.
func (f *Fulfiller) render(ctx context.Context, orderID string) error {
	rows, err := f.tickets(ctx, orderID)
	if err != nil {
		return err
	}
	versions := 0
	for _, t := range rows {
		versions += t.Version
		if t.Rendered {
			continue
		}
		s, err := f.Issuer.Open(t.Sealed)
		if err != nil {
			return err
		}
		pdf, err := tickets.Render(tickets.Doc{
			Organizer: t.Organizer, EventTitle: t.Title, StartsAt: t.StartsAt, TimeZone: t.TimeZone, Venue: t.Venue,
			TicketType: t.TicketType, Ordinal: t.Ordinal, Of: t.Of, OrderRef: t.OrderRef,
			FallbackCode: s.Fallback, QRContent: f.GateOrigin + "/scan#" + s.Credential,
		})
		if err != nil {
			return err
		}
		sum := sha256.Sum256(pdf)
		if _, err := f.DB.Exec(ctx, `INSERT INTO ticket_document (ticket_id, version, pdf, size_bytes, sha256)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, t.ID, t.Version, pdf, len(pdf), sum[:]); err != nil {
			return err
		}
	}
	// Keyed by the credential versions: a repeated render sends nothing new,
	// but a reissued ticket (a higher version) is delivered again.
	return queueDelivery(ctx, f.DB, orderID, fmt.Sprintf("docs-%d", versions))
}

func queueDelivery(ctx context.Context, db dbx.DB, orderID, tag string) error {
	for _, kind := range []string{"deliver_email", "deliver_sms"} {
		if err := outbox.Enqueue(ctx, db, kind, orderID, orderID+":"+tag); err != nil {
			return err
		}
	}
	return nil
}

type contact struct {
	Ref      string    `db:"public_ref"`
	Email    string    `db:"email"`
	Phone    string    `db:"phone"`
	Title    string    `db:"title"`
	StartsAt time.Time `db:"starts_at"`
	TimeZone string    `db:"time_zone"`
	Sent     int       `db:"sent"`
}

func (f *Fulfiller) contact(ctx context.Context, orderID, channel string) (contact, error) {
	return dbx.One[contact](ctx, f.DB, `
		SELECT o.public_ref, o.email, o.phone, e.title, e.starts_at, e.time_zone,
			(SELECT count(*) FROM delivery_attempt a WHERE a.order_id = o.id AND a.channel = $2)::int AS sent
		FROM ticket_order o JOIN event e ON e.id = o.event_id WHERE o.id = $1`, orderID, channel)
}

func when(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Mon 2 Jan 2006, 15:04 MST")
}

// recordAttempt logs one send and passes the send error through.
func (f *Fulfiller) recordAttempt(ctx context.Context, orderID, channel, providerID string, sendErr error) error {
	status, msg := "sent", ""
	if sendErr != nil {
		status, msg = "failed", sendErr.Error()
	}
	if _, err := f.DB.Exec(ctx, `INSERT INTO delivery_attempt (order_id, channel, status, provider_id, error)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''))`, orderID, channel, status, providerID, msg); err != nil {
		return err
	}
	return sendErr
}

// email sends every ticket PDF, in numbered emails of at most
// attachmentsPerEmail files. A retry resends the same tickets.
func (f *Fulfiller) email(ctx context.Context, orderID string) error {
	rows, err := f.tickets(ctx, orderID)
	if err != nil {
		return err
	}
	c, err := f.contact(ctx, orderID, "email")
	if err != nil {
		return err
	}
	var atts []delivery.Attachment
	for _, t := range rows {
		file, err := tickets.Document(ctx, f.DB, t.ID)
		if errors.Is(err, tickets.ErrNotFound) {
			return ErrNotReady
		}
		if err != nil {
			return err
		}
		atts = append(atts, delivery.Attachment{Name: file.Name(), ContentType: "application/pdf", Data: file.PDF,
			TicketID: t.ID, Version: t.Version})
	}
	parts := (len(atts) + attachmentsPerEmail - 1) / attachmentsPerEmail
	for p := range parts {
		batch := atts[p*attachmentsPerEmail : min((p+1)*attachmentsPerEmail, len(atts))]
		subject := fmt.Sprintf("Your %d tickets for %s", len(atts), c.Title)
		if parts > 1 {
			subject += fmt.Sprintf(" (email %d of %d)", p+1, parts)
		}
		text := fmt.Sprintf("Order %s\n%s\n%s\n\nEach attached PDF is one ticket and admits one person, once. "+
			"Show the QR code at the entrance, on screen or printed. Keep your tickets private: the first person "+
			"to present a ticket is admitted.\n", c.Ref, c.Title, when(c.StartsAt, c.TimeZone))
		domain := strings.TrimPrefix(strings.TrimPrefix(f.PublicOrigin, "https://"), "http://")
		e := delivery.Email{To: c.Email, Subject: subject, Text: text, Attachments: batch,
			MessageID: fmt.Sprintf("<%s.%d.%d@%s>", c.Ref, c.Sent+1, p+1, domain)}
		id, sendErr := f.Mailer.Send(ctx, e)
		if err := f.recordAttempt(ctx, orderID, "email", id, sendErr); err != nil {
			return err
		}
	}
	return nil
}

// sms creates a private pickup link and texts it. Only the link secret's
// SHA-256 is stored; the link stays valid until two days after the event.
func (f *Fulfiller) sms(ctx context.Context, orderID string) error {
	c, err := f.contact(ctx, orderID, "sms")
	if err != nil {
		return err
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(secret))
	if _, err := f.DB.Exec(ctx, `INSERT INTO download_grant (order_id, secret_digest, expires_at) VALUES ($1, $2, $3)`,
		orderID, sum[:], c.StartsAt.Add(grantGrace)); err != nil {
		return err
	}
	text := fmt.Sprintf("Kacheem Comms: your tickets for %s, %s. Download them: %s/pickup/#%s",
		c.Title, when(c.StartsAt, c.TimeZone), f.PublicOrigin, secret)
	id, sendErr := f.SMS.Send(ctx, c.Phone, text)
	return f.recordAttempt(ctx, orderID, "sms", id, sendErr)
}

// Resend queues the original tickets again on both channels, at most once
// per ResendCooldown. Only paid orders have tickets to resend.
func Resend(ctx context.Context, db dbx.DB, orderID string) error {
	return dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		var recent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbox_job WHERE order_id = $1 AND kind = 'deliver_email'
			AND dedupe_key LIKE '%:resend:%' AND created_at > now() - make_interval(secs => $2))
			FROM ticket_order WHERE id = $1 FOR UPDATE`, orderID, ResendCooldown.Seconds()).Scan(&recent); err != nil {
			return err
		}
		if recent {
			return ErrTooSoon
		}
		return queueDelivery(ctx, tx, orderID, fmt.Sprintf("resend:%d", time.Now().UnixNano()))
	})
}

// Delivery is the latest outcome on one channel.
type Delivery struct {
	Channel string    `db:"channel"`
	Status  string    `db:"status"`
	At      time.Time `db:"created_at"`
}

// Deliveries returns the latest email and SMS outcome for an order.
func Deliveries(ctx context.Context, db dbx.DB, orderID string) ([]Delivery, error) {
	return dbx.All[Delivery](ctx, db, `SELECT DISTINCT ON (channel) channel, status, created_at
		FROM delivery_attempt WHERE order_id = $1 ORDER BY channel, id DESC`, orderID)
}
