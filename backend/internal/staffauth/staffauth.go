package staffauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
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
	DB       dbx.DB
	webauthn *webauthn.WebAuthn
}

// New returns a Service whose WebAuthn relying party is the staff origin,
// e.g. "https://gate.example.org".
func New(db dbx.DB, staffOrigin string) (*Service, error) {
	u, err := url.Parse(staffOrigin)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("staffauth: staff origin %q is not a URL", staffOrigin)
	}
	// The configuration is complete, so webauthn.New cannot fail here.
	w, _ := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Kacheem Comms staff",
		RPOrigins:     []string{staffOrigin},
	})
	return &Service{DB: db, webauthn: w}, nil
}

// Staff is a staff member; it is the WebAuthn user.
type Staff struct {
	ID     string `db:"id"`
	Email  string `db:"email"`
	Name   string `db:"name"`
	Role   string `db:"role"`
	Status string `db:"status"`
	Epoch  int    `db:"session_epoch"`
	creds  []webauthn.Credential
}

func (s Staff) WebAuthnID() []byte                         { return []byte(s.ID) }
func (s Staff) WebAuthnName() string                       { return s.Email }
func (s Staff) WebAuthnDisplayName() string                { return s.Name }
func (s Staff) WebAuthnCredentials() []webauthn.Credential { return s.creds }

const staffColumns = `id::text, email, name, role, status, session_epoch`

func (s *Service) load(ctx context.Context, db dbx.DB, where string, arg any) (Staff, error) {
	st, err := dbx.One[Staff](ctx, db, `SELECT `+staffColumns+` FROM staff WHERE `+where, arg)
	if err != nil {
		return Staff{}, err
	}
	rows, err := dbx.All[struct {
		ID        []byte `db:"id"`
		PublicKey []byte `db:"public_key"`
		Count     int64  `db:"sign_count"`
		AAGUID    []byte `db:"aaguid"`
		BE        bool   `db:"backup_eligible"`
		BS        bool   `db:"backup_state"`
	}](ctx, db, `SELECT id, public_key, sign_count, aaguid, backup_eligible, backup_state
		FROM webauthn_credential WHERE staff_id = $1`, st.ID)
	if err != nil {
		return Staff{}, err
	}
	for _, r := range rows {
		st.creds = append(st.creds, webauthn.Credential{ID: r.ID, PublicKey: r.PublicKey,
			Flags:         webauthn.CredentialFlags{BackupEligible: r.BE, BackupState: r.BS},
			Authenticator: webauthn.Authenticator{AAGUID: r.AAGUID, SignCount: uint32(r.Count)}})
	}
	return st, nil
}

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

// Ceremony is a WebAuthn challenge to send to the browser, and the server
// state to keep (in the session) until the browser answers.
type Ceremony struct {
	StaffID string
	Options json.RawMessage
	State   []byte
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

// BeginInvite sets the invited person's password and returns the
// registration challenge for their security key.
func (s *Service) BeginInvite(ctx context.Context, token, password string) (Ceremony, error) {
	if len([]rune(password)) < MinPasswordLen {
		return Ceremony{}, ErrPassword
	}
	id, err := invited(ctx, s.DB, token, "")
	if err != nil {
		return Ceremony{}, err
	}
	if _, err := s.DB.Exec(ctx, `UPDATE staff SET password_hash = $2 WHERE id = $1`, id, HashPassword(password)); err != nil {
		return Ceremony{}, err
	}
	st, err := s.load(ctx, s.DB, `id = $1`, id)
	if err != nil {
		return Ceremony{}, err
	}
	options, session, _ := s.webauthn.BeginRegistration(st)
	return ceremony(id, options, session), nil
}

func ceremony(id string, options any, session *webauthn.SessionData) Ceremony {
	o, _ := json.Marshal(options)
	st, _ := json.Marshal(session)
	return Ceremony{StaffID: id, Options: o, State: st}
}

// FinishInvite verifies the new security key, stores it and activates the
// account. The invitation cannot be used again.
func (s *Service) FinishInvite(ctx context.Context, token string, state []byte, response io.Reader) (Staff, error) {
	parsed, err := protocol.ParseCredentialCreationResponseBody(response)
	if err != nil {
		return Staff{}, ErrLogin
	}
	var session webauthn.SessionData
	_ = json.Unmarshal(state, &session)
	var st Staff
	err = dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		id, err := invited(ctx, tx, token, " FOR UPDATE")
		if err != nil {
			return err
		}
		if st, err = s.load(ctx, tx, `id = $1`, id); err != nil {
			return err
		}
		cred, err := s.webauthn.CreateCredential(st, session, parsed)
		if err != nil {
			return ErrLogin
		}
		if _, err := tx.Exec(ctx, `INSERT INTO webauthn_credential (id, staff_id, public_key, attestation, sign_count, aaguid,
				backup_eligible, backup_state) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			cred.ID, id, cred.PublicKey, cred.AttestationType, cred.Authenticator.SignCount, cred.Authenticator.AAGUID,
			cred.Flags.BackupEligible, cred.Flags.BackupState); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE staff_invite SET used_at = now() WHERE token_digest = $1`, tickets.Digest(token)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE staff SET status = 'active' WHERE id = $1`, id); err != nil {
			return err
		}
		st.Status = "active"
		return audit.Record(ctx, tx, audit.Staff(id), "staff.activate", "staff", id, "", nil)
	})
	return st, err
}

// BeginLogin checks the email and password and returns the security-key
// challenge. Every failure is ErrLogin, after the same password-hash work.
func (s *Service) BeginLogin(ctx context.Context, email, password string) (Ceremony, error) {
	st, err := s.load(ctx, s.DB, `email = $1`, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, pgx.ErrNoRows) {
		CheckPassword(dummyHash, password)
		return Ceremony{}, ErrLogin
	}
	if err != nil {
		return Ceremony{}, err
	}
	var hash *string
	if err := s.DB.QueryRow(ctx, `SELECT password_hash FROM staff WHERE id = $1`, st.ID).Scan(&hash); err != nil {
		return Ceremony{}, err
	}
	if hash == nil || !CheckPassword(*hash, password) || st.Status != "active" || len(st.creds) == 0 {
		return Ceremony{}, ErrLogin
	}
	options, session, _ := s.webauthn.BeginLogin(st)
	return ceremony(st.ID, options, session), nil
}

// FinishLogin verifies the security-key assertion and returns the signed-in
// staff member, whose Epoch the session must carry.
func (s *Service) FinishLogin(ctx context.Context, staffID string, state []byte, response io.Reader) (Staff, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBody(response)
	if err != nil {
		return Staff{}, ErrLogin
	}
	var session webauthn.SessionData
	_ = json.Unmarshal(state, &session)
	st, err := s.load(ctx, s.DB, `id::text = $1`, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Staff{}, ErrLogin
	}
	if err != nil {
		return Staff{}, err
	}
	cred, err := s.webauthn.ValidateLogin(st, session, parsed)
	if err != nil || st.Status != "active" {
		return Staff{}, ErrLogin
	}
	err = dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE webauthn_credential SET sign_count = $2, last_used_at = now() WHERE id = $1`,
			cred.ID, cred.Authenticator.SignCount); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Staff(st.ID), "staff.sign_in", "staff", st.ID, "", nil)
	})
	return st, err
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
