package tickets

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
)

// ErrNotFound means no such ticket, or its PDF is not rendered yet.
var ErrNotFound = errors.New("tickets: not found or not ready")

// Info is a ticket as listed to its buyer.
type Info struct {
	ID      string `db:"id"`
	Ordinal int    `db:"ordinal"`
	Of      int    `db:"of"`
	Status  string `db:"status"`
	Ready   bool   `db:"ready"`
}

// ForOrder lists an order's tickets; Ready means its current PDF exists.
func ForOrder(ctx context.Context, db dbx.DB, orderID string) ([]Info, error) {
	return dbx.All[Info](ctx, db, `
		SELECT t.id::text, t.ordinal, count(*) OVER ()::int AS of, t.status,
			EXISTS (SELECT 1 FROM ticket_document d WHERE d.ticket_id = t.id AND d.version = t.credential_version) AS ready
		FROM ticket t WHERE t.order_id = $1 ORDER BY t.ordinal`, orderID)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// File is a rendered ticket ready to stream.
type File struct {
	OrderID string `db:"order_id"`
	BuyerID string `db:"buyer_id"`
	Ordinal int    `db:"ordinal"`
	Of      int    `db:"of"`
	PDF     []byte `db:"pdf"`
}

// Name is the download file name, e.g. "ticket-1-of-3.pdf".
func (f File) Name() string { return fmt.Sprintf("ticket-%d-of-%d.pdf", f.Ordinal, f.Of) }

// Document returns a ticket's current PDF with the order it belongs to, so the
// caller can check the requester may see it.
func Document(ctx context.Context, db dbx.DB, ticketID string) (File, error) {
	if !uuidPattern.MatchString(ticketID) {
		return File{}, ErrNotFound
	}
	f, err := dbx.One[File](ctx, db, `
		SELECT t.order_id::text, o.buyer_id, t.ordinal,
			(SELECT count(*) FROM ticket x WHERE x.order_id = t.order_id)::int AS of, d.pdf
		FROM ticket t
		JOIN ticket_order o ON o.id = t.order_id
		JOIN ticket_document d ON d.ticket_id = t.id AND d.version = t.credential_version
		WHERE t.id = $1`, ticketID)
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, ErrNotFound
	}
	return f, err
}

// ErrGrantInvalid means a pickup secret is unknown, revoked or expired.
var ErrGrantInvalid = errors.New("tickets: pickup link is invalid or has expired")

// Redeem checks a pickup secret and returns the order it grants access to.
func Redeem(ctx context.Context, db dbx.DB, secret string) (string, error) {
	var orderID string
	err := db.QueryRow(ctx, `SELECT order_id::text FROM download_grant
		WHERE secret_digest = $1 AND revoked_at IS NULL AND expires_at > now()`, Digest(secret)).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrGrantInvalid
	}
	return orderID, err
}
