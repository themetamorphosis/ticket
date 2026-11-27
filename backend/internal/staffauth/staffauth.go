package staffauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/tickets"
)

var (
	// ErrLogin is the single answer for any failed sign-in step, so it does
	// not reveal whether an email has an account.
	ErrLogin = errors.New("staffauth: sign-in failed")
	// ErrInvite means an invitation link is unknown, used or expired.
	ErrInvite = errors.New("staffauth: invitation is not valid")
	// ErrPassword means a new password is too short.
	ErrPassword = fmt.Errorf("staffauth: use a password of at least %d characters", MinPasswordLen)
	// ErrRevoked means a session's account was disabled or changed.
	ErrRevoked = errors.New("staffauth: session revoked")
	// ErrInput means invalid invitation details.
	ErrInput = errors.New("staffauth: invalid staff details")
	// ErrExists means an active account already uses that email.
	ErrExists = errors.New("staffauth: an account with that email exists")
)

// Roles, in the plan's section 7.
var Roles = map[string]bool{"organizer": true, "finance": true, "support": true, "gate": true}

// InviteTTL is how long an invitation link works.
const InviteTTL = 72 * time.Hour

// Service runs staff invitations, sign-in and session checks.
type Service struct {
	DB dbx.DB
}

// New returns a Service.
func New(db dbx.DB) *Service { return &Service{DB: db} }

// Staff is a staff member.
type Staff struct {
	ID     string `db:"id"`
	Email  string `db:"email"`
	Name   string `db:"name"`
	Role   string `db:"role"`
	Status string `db:"status"`
	Epoch  int    `db:"session_epoch"`
}

const staffColumns = `id::text, email, name, role, status, session_epoch`

func secret() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Invite creates (or refreshes) an invited account and returns the one-time
// token for its invitation link.
func (s *Service) Invite(ctx context.Context, actor audit.Actor, email, name, role string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if !Roles[role] || name == "" || !strings.Contains(email, "@") {
		return "", ErrInput
	}
	token := secret()
	err := dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var id, status string
		err := tx.QueryRow(ctx, `INSERT INTO staff (email, name, role) VALUES ($1, $2, $3)
			ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name, role = EXCLUDED.role
			RETURNING id::text, status`, email, name, role).Scan(&id, &status)
		if err != nil {
			return err
		}
		if status != "invited" {
			return ErrExists
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_invite (token_digest, staff_id, expires_at)
			VALUES ($1, $2, now() + make_interval(secs => $3))`, tickets.Digest(token), id, InviteTTL.Seconds()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, actor, "staff.invite", "staff", id, "", map[string]any{"role": role})
	})
	return token, err
}

func invited(ctx context.Context, db dbx.DB, token, lock string) (string, error) {
	var id string
	err := db.QueryRow(ctx, `SELECT staff_id::text FROM staff_invite
		WHERE token_digest = $1 AND used_at IS NULL AND expires_at > now()`+lock, tickets.Digest(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvite
	}
	return id, err
}

// AcceptInvite sets the invited person's password, activates the account and
// returns it. The invitation cannot be used again.
func (s *Service) AcceptInvite(ctx context.Context, token, password string) (Staff, error) {
	if len([]rune(password)) < MinPasswordLen {
		return Staff{}, ErrPassword
	}
	// Refuse a bad link before spending the hashing work, and hash outside the
	// transaction so the invitation row is not locked meanwhile.
	if _, err := invited(ctx, s.DB, token, ""); err != nil {
		return Staff{}, err
	}
	hash := HashPassword(password)
	var st Staff
	err := dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		id, err := invited(ctx, tx, token, " FOR UPDATE")
		if err != nil {
			return err
		}
		if st, err = dbx.One[Staff](ctx, tx, `UPDATE staff SET password_hash = $2, status = 'active'
			WHERE id = $1 RETURNING `+staffColumns, id, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE staff_invite SET used_at = now() WHERE token_digest = $1`, tickets.Digest(token)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Staff(id), "staff.activate", "staff", id, "", nil)
	})
	if err != nil {
		return Staff{}, err
	}
	return st, nil
}

// SignIn checks the email and password and returns the signed-in staff
// member, whose Epoch the session must carry. Every refusal is ErrLogin,
// after the same password-hash work.
func (s *Service) SignIn(ctx context.Context, email, password string) (Staff, error) {
	row, err := dbx.One[struct {
		Staff
		Hash *string `db:"password_hash"`
	}](ctx, s.DB, `SELECT `+staffColumns+`, password_hash FROM staff WHERE email = $1`,
		strings.ToLower(strings.TrimSpace(email)))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Staff{}, err
	}
	// Unknown and not-yet-accepted accounts have no hash.
	hash := dummyHash
	if row.Hash != nil {
		hash = *row.Hash
	}
	if !CheckPassword(hash, password) || row.Status != "active" {
		return Staff{}, ErrLogin
	}
	err = dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Staff(row.ID), "staff.sign_in", "staff", row.ID, "", nil)
	})
	if err != nil {
		return Staff{}, err
	}
	return row.Staff, nil
}

// Current returns the staff member for a session, refusing a disabled account
// or a session from before the last revocation.
func (s *Service) Current(ctx context.Context, staffID string, epoch int) (Staff, error) {
	st, err := dbx.One[Staff](ctx, s.DB, `SELECT `+staffColumns+` FROM staff WHERE id::text = $1`, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Staff{}, ErrRevoked
	}
	if err != nil {
		return Staff{}, err
	}
	if st.Status != "active" || st.Epoch != epoch {
		return Staff{}, ErrRevoked
	}
	return st, nil
}

// Disable revokes a staff member: the account stops working and every
// session they hold ends at their next request.
func (s *Service) Disable(ctx context.Context, actor audit.Actor, staffID, reason string) error {
	return dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE staff SET status = 'disabled', session_epoch = session_epoch + 1
			WHERE id::text = $1`, staffID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrInput
		}
		return audit.Record(ctx, tx, actor, "staff.disable", "staff", staffID, reason, nil)
	})
}

// List returns every staff account.
func (s *Service) List(ctx context.Context) ([]Staff, error) {
	return dbx.All[Staff](ctx, s.DB, `SELECT `+staffColumns+` FROM staff ORDER BY role, name`)
}
