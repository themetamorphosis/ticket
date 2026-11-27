// Package delivery sends ticket emails and SMS messages. Email goes over SMTP,
// which works with a transactional email provider's relay (the plan's
// default) and with self-hosted Postfix. SMS goes through a carrier or
// aggregator adapter once the Bangladeshi sender ID is registered; until then
// the development channel records messages for the /dev/outbox page.
package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"kacheem/backend/internal/config"
	"kacheem/backend/internal/dbx"
)

// Attachment is one file on an email.
type Attachment struct {
	Name        string
	ContentType string
	Data        []byte
	// TicketID and Version identify a ticket PDF, for the development log.
	TicketID string
	Version  int
}

// Email is one message to one recipient.
type Email struct {
	To          string
	Subject     string
	Text        string
	Attachments []Attachment
	// MessageID is set by the caller so retries of one send share an ID.
	MessageID string
}

// Mailer sends email. It returns a provider message ID.
type Mailer interface {
	Send(ctx context.Context, e Email) (string, error)
}

// SMS sends a text message to an E.164 number. It returns a provider ID.
type SMS interface {
	Send(ctx context.Context, to, text string) (string, error)
}

// DevMailer records emails in dev_message instead of sending them.
type DevMailer struct{ DB dbx.DB }

// DevSMS records SMS messages in dev_message instead of sending them.
type DevSMS struct{ DB dbx.DB }

type devAttachment struct {
	Name     string `json:"name"`
	TicketID string `json:"ticket_id"`
	Version  int    `json:"version"`
}

func (m DevMailer) Send(ctx context.Context, e Email) (string, error) {
	atts := make([]devAttachment, len(e.Attachments))
	for i, a := range e.Attachments {
		atts[i] = devAttachment{a.Name, a.TicketID, a.Version}
	}
	b, _ := json.Marshal(atts)
	return record(ctx, m.DB, "email", e.To, e.Subject, e.Text, string(b))
}

func (s DevSMS) Send(ctx context.Context, to, text string) (string, error) {
	return record(ctx, s.DB, "sms", to, "", text, "[]")
}

func record(ctx context.Context, db dbx.DB, channel, to, subject, body, atts string) (string, error) {
	var id int64
	err := db.QueryRow(ctx, `INSERT INTO dev_message (channel, recipient, subject, body, attachments)
		VALUES ($1, $2, $3, $4, $5::jsonb) RETURNING id`, channel, to, subject, body, atts).Scan(&id)
	return fmt.Sprintf("dev-%d", id), err
}

// NewMailer returns the configured email channel.
func NewMailer(cfg config.Delivery, db dbx.DB) Mailer {
	if cfg.EmailProvider == "smtp" {
		return SMTP{Addr: cfg.SMTPAddr, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.EmailFrom,
			Timeout: 60 * time.Second}
	}
	return DevMailer{DB: db}
}

// NewSMS returns the configured SMS channel (development only, for now).
func NewSMS(cfg config.Delivery, db dbx.DB) SMS { return DevSMS{DB: db} }

// UsesDevChannels reports whether /dev/outbox has anything to show.
func UsesDevChannels(cfg config.Delivery) bool {
	return cfg.EmailProvider != "smtp" || cfg.SMSProvider == "fake"
}
