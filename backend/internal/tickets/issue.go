package tickets

import (
	"context"
	"encoding/json"
	"errors"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/outbox"
	"kacheem/backend/internal/sealbox"
)

// Issuer creates and opens ticket credentials.
type Issuer struct{ Box *sealbox.Box }

type sealed struct {
	Credential string `json:"c"`
	Fallback   string `json:"f"`
}

// Secrets are a ticket's plaintext credential and fallback code.
type Secrets struct {
	Credential string
	Fallback   string
}

// Issue creates one ticket per paid place and queues rendering. Call it in the
// transaction that marks the order paid, so a paid order always has exactly
// its tickets and its fulfilment job. It is safe to repeat.
func (i *Issuer) Issue(ctx context.Context, tx dbx.DB, orderID string) error {
	lines, err := dbx.All[struct {
		TicketTypeID string `db:"ticket_type_id"`
		Quantity     int    `db:"quantity"`
	}](ctx, tx, `SELECT ticket_type_id::text, quantity FROM order_line WHERE order_id = $1 ORDER BY ticket_type_id`, orderID)
	if err != nil {
		return err
	}
	ordinal := 0
	for _, l := range lines {
		for range l.Quantity {
			ordinal++
			s := sealed{Credential: NewCredential(), Fallback: NewFallbackCode()}
			norm, _ := NormalizeFallback(s.Fallback)
			plain, _ := json.Marshal(s)
			if _, err := tx.Exec(ctx, `INSERT INTO ticket (order_id, ticket_type_id, ordinal,
					credential_digest, fallback_digest, credential_sealed)
				VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (order_id, ordinal) DO NOTHING`,
				orderID, l.TicketTypeID, ordinal, Digest(s.Credential), Digest(norm), i.Box.Seal(plain)); err != nil {
				return err
			}
		}
	}
	return outbox.Enqueue(ctx, tx, "render_tickets", orderID, orderID)
}

// ErrSealed means stored ticket secrets could not be opened (wrong key).
var ErrSealed = errors.New("tickets: cannot open ticket secrets")

// Open returns a ticket's secrets for rendering.
func (i *Issuer) Open(data []byte) (Secrets, error) {
	plain, err := i.Box.Open(data)
	if err != nil {
		return Secrets{}, ErrSealed
	}
	var s sealed
	_ = json.Unmarshal(plain, &s)
	return Secrets{Credential: s.Credential, Fallback: s.Fallback}, nil
}
