package staffauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/descope/virtualwebauthn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

const origin = "https://gate.test"

var rp = virtualwebauthn.RelyingParty{ID: "gate.test", Name: "Kacheem Comms staff", Origin: origin}

// key is a person's security key.
type key struct {
	auth virtualwebauthn.Authenticator
	cred virtualwebauthn.Credential
}

func newKey() *key {
	return &key{auth: virtualwebauthn.NewAuthenticator(), cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)}
}

func (k *key) register(t *testing.T, c Ceremony) string {
	t.Helper()
	opts, err := virtualwebauthn.ParseAttestationOptions(string(c.Options))
	if err != nil {
		t.Fatal(err)
	}
	resp := virtualwebauthn.CreateAttestationResponse(rp, k.auth, k.cred, *opts)
	k.auth.AddCredential(k.cred)
	return resp
}

func (k *key) sign(t *testing.T, c Ceremony) string {
	t.Helper()
	opts, err := virtualwebauthn.ParseAssertionOptions(string(c.Options))
	if err != nil {
		t.Fatal(err)
	}
	return virtualwebauthn.CreateAssertionResponse(rp, k.auth, k.cred, *opts)
}

func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Pool(t)
	s, err := New(pool, origin)
	if err != nil {
		t.Fatal(err)
	}
	return s, pool
}

const password = "correct horse battery staple"

// enroll invites someone and completes their invitation with a new key.
func enroll(t *testing.T, s *Service, email, role string) (Staff, *key) {
	t.Helper()
	token, err := s.Invite(ctx, audit.Actor{Kind: "system"}, email, "Test "+role, role)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.BeginInvite(ctx, token, password)
	if err != nil {
		t.Fatal(err)
	}
	k := newKey()
	st, err := s.FinishInvite(ctx, token, c.State, strings.NewReader(k.register(t, c)))
	if err != nil {
		t.Fatalf("FinishInvite: %v", err)
	}
	return st, k
}

func TestPasswords(t *testing.T) {
	h := HashPassword("s3cret password")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") || h == HashPassword("s3cret password") {
		t.Fatalf("hash %s", h)
	}
	if !CheckPassword(h, "s3cret password") || CheckPassword(h, "s3cret passwore") {
		t.Fatal("check")
	}
	parts := strings.Split(h, "$")
	for _, bad := range []string{"", "$bcrypt$x$y$z$w", "$argon2id$v=19$m=x$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=65536,t=3,p=2$!!$" + parts[5], "$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$",
		"$argon2id$v=18$m=65536,t=3,p=2$" + parts[4] + "$" + parts[5]} {
		if CheckPassword(bad, "s3cret password") {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestInviteRegisterAndSignIn(t *testing.T) {
	s, pool := newService(t)
	st, k := enroll(t, s, " Gate@Example.org ", "gate")
	if st.Status != "active" || st.Email != "gate@example.org" || st.Role != "gate" || st.Epoch != 1 {
		t.Fatalf("staff %+v", st)
	}
	c, err := s.BeginLogin(ctx, "GATE@example.org", password)
	if err != nil || c.StaffID != st.ID {
		t.Fatalf("BeginLogin %v", err)
	}
	signedIn, err := s.FinishLogin(ctx, c.StaffID, c.State, strings.NewReader(k.sign(t, c)))
	if err != nil || signedIn.ID != st.ID {
		t.Fatalf("FinishLogin %+v %v", signedIn, err)
	}
	cur, err := s.Current(ctx, st.ID, 1)
	if err != nil || cur.Name != "Test gate" {
		t.Fatalf("Current %+v %v", cur, err)
	}
	// Many passkeys always report a signature counter of 0, so the use time
	// is what shows the key was used.
	var used bool
	var audits int
	_ = pool.QueryRow(ctx, `SELECT last_used_at IS NOT NULL FROM webauthn_credential`).Scan(&used)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE action IN ('staff.invite', 'staff.activate', 'staff.sign_in')`).Scan(&audits)
	if !used || audits != 3 {
		t.Fatalf("used %v, audits %d", used, audits)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List %v %v", list, err)
	}
}

func TestSignInFailuresAreIndistinguishable(t *testing.T) {
	s, pool := newService(t)
	_, k := enroll(t, s, "gate@example.org", "gate")
	other := newKey()
	other.cred = virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	for name, try := range map[string]func() error{
		"unknown email":  func() error { _, err := s.BeginLogin(ctx, "nobody@example.org", password); return err },
		"wrong password": func() error { _, err := s.BeginLogin(ctx, "gate@example.org", "wrong password!!"); return err },
		"wrong key": func() error {
			c, _ := s.BeginLogin(ctx, "gate@example.org", password)
			other.auth.AddCredential(other.cred)
			opts, _ := virtualwebauthn.ParseAssertionOptions(string(c.Options))
			resp := virtualwebauthn.CreateAssertionResponse(rp, other.auth, other.cred, *opts)
			_, err := s.FinishLogin(ctx, c.StaffID, c.State, strings.NewReader(resp))
			return err
		},
		"garbage response": func() error {
			c, _ := s.BeginLogin(ctx, "gate@example.org", password)
			_, err := s.FinishLogin(ctx, c.StaffID, c.State, strings.NewReader("{}"))
			return err
		},
		"replayed assertion with another challenge": func() error {
			c1, _ := s.BeginLogin(ctx, "gate@example.org", password)
			c2, _ := s.BeginLogin(ctx, "gate@example.org", password)
			_, err := s.FinishLogin(ctx, c2.StaffID, c2.State, strings.NewReader(k.sign(t, c1)))
			return err
		},
		"unknown staff id": func() error {
			c, _ := s.BeginLogin(ctx, "gate@example.org", password)
			_, err := s.FinishLogin(ctx, "00000000-0000-0000-0000-000000000000", c.State, strings.NewReader(k.sign(t, c)))
			return err
		},
	} {
		if err := try(); !errors.Is(err, ErrLogin) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// An invited account without a key cannot sign in.
	_, _ = s.Invite(ctx, audit.Actor{Kind: "system"}, "new@example.org", "New", "support")
	if _, err := s.BeginLogin(ctx, "new@example.org", password); !errors.Is(err, ErrLogin) {
		t.Fatalf("invited: %v", err)
	}
	_ = pool
}

func TestInvitationRules(t *testing.T) {
	s, pool := newService(t)
	sys := audit.Actor{Kind: "system"}
	for _, bad := range [][3]string{{"no-at-sign", "Name", "gate"}, {"a@example.org", " ", "gate"}, {"a@example.org", "Name", "admin"}} {
		if _, err := s.Invite(ctx, sys, bad[0], bad[1], bad[2]); !errors.Is(err, ErrInput) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	token, _ := s.Invite(ctx, sys, "a@example.org", "A", "finance")
	if _, err := s.BeginInvite(ctx, token, "short"); !errors.Is(err, ErrPassword) {
		t.Fatalf("short password: %v", err)
	}
	if _, err := s.BeginInvite(ctx, "not-a-token", password); !errors.Is(err, ErrInvite) {
		t.Fatalf("bad token: %v", err)
	}
	c, _ := s.BeginInvite(ctx, token, password)
	if _, err := s.FinishInvite(ctx, token, c.State, strings.NewReader("not json")); !errors.Is(err, ErrLogin) {
		t.Fatalf("garbage: %v", err)
	}
	// A key answering a different challenge is refused.
	c2, _ := s.BeginInvite(ctx, token, password)
	k := newKey()
	if _, err := s.FinishInvite(ctx, token, c2.State, strings.NewReader(k.register(t, c))); !errors.Is(err, ErrLogin) {
		t.Fatalf("wrong challenge: %v", err)
	}
	if _, err := s.FinishInvite(ctx, token, c2.State, strings.NewReader(newKey().register(t, c2))); err != nil {
		t.Fatal(err)
	}
	// Used links do not work again, and active accounts are not re-invited.
	if _, err := s.BeginInvite(ctx, token, password); !errors.Is(err, ErrInvite) {
		t.Fatalf("reused: %v", err)
	}
	if _, err := s.Invite(ctx, sys, "a@example.org", "A", "organizer"); !errors.Is(err, ErrExists) {
		t.Fatalf("re-invite active: %v", err)
	}
	var role string
	_ = pool.QueryRow(ctx, `SELECT role FROM staff WHERE email = 'a@example.org'`).Scan(&role)
	if role != "finance" {
		t.Fatal("re-invite changed an active account's role")
	}
	// Expired links do not work.
	expired, _ := s.Invite(ctx, sys, "b@example.org", "B", "gate")
	_, _ = pool.Exec(ctx, `UPDATE staff_invite SET expires_at = now() - interval '1 second' WHERE used_at IS NULL`)
	if _, err := s.BeginInvite(ctx, expired, password); !errors.Is(err, ErrInvite) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := New(pool, "::not a url"); err == nil {
		t.Fatal("bad origin accepted")
	}
}

func TestDisableRevokesEverySession(t *testing.T) {
	s, _ := newService(t)
	st, k := enroll(t, s, "gate@example.org", "gate")
	if err := s.Disable(ctx, audit.Actor{Kind: "system"}, st.ID, "lost device"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Current(ctx, st.ID, 1); !errors.Is(err, ErrRevoked) {
		t.Fatalf("old session: %v", err)
	}
	if _, err := s.Current(ctx, st.ID, 2); !errors.Is(err, ErrRevoked) {
		t.Fatalf("disabled account: %v", err)
	}
	if _, err := s.Current(ctx, "00000000-0000-0000-0000-000000000000", 1); !errors.Is(err, ErrRevoked) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.BeginLogin(ctx, "gate@example.org", password); !errors.Is(err, ErrLogin) {
		t.Fatalf("disabled sign-in: %v", err)
	}
	if err := s.Disable(ctx, audit.Actor{Kind: "system"}, "00000000-0000-0000-0000-000000000000", "x"); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown disable: %v", err)
	}
	_ = k
}

func TestSignedInAccountDisabledMidCeremony(t *testing.T) {
	s, pool := newService(t)
	_, k := enroll(t, s, "gate@example.org", "gate")
	c, _ := s.BeginLogin(ctx, "gate@example.org", password)
	_, _ = pool.Exec(ctx, `UPDATE staff SET status = 'disabled'`)
	if _, err := s.FinishLogin(ctx, c.StaffID, c.State, strings.NewReader(k.sign(t, c))); !errors.Is(err, ErrLogin) {
		t.Fatalf("err = %v", err)
	}
}

func TestFaults(t *testing.T) {
	s, pool := newService(t)
	_, k := enroll(t, s, "gate@example.org", "gate")
	with := func(db dbx.DB) *Service { c := *s; c.DB = db; return &c }
	n := 0
	ops := []func(db dbx.DB) error{
		func(db dbx.DB) error {
			n++
			_, err := with(db).Invite(ctx, audit.Actor{Kind: "system"}, strings.Repeat("x", n)+"@example.org", "X", "gate")
			return err
		},
		func(db dbx.DB) error {
			token, _ := s.Invite(ctx, audit.Actor{Kind: "system"}, "f@example.org", "F", "gate")
			_, err := with(db).BeginInvite(ctx, token, password)
			return err
		},
		func(db dbx.DB) error {
			n++
			email := strings.Repeat("y", n) + "@example.org"
			token, _ := s.Invite(ctx, audit.Actor{Kind: "system"}, email, "Y", "gate")
			c, _ := s.BeginInvite(ctx, token, password)
			_, err := with(db).FinishInvite(ctx, token, c.State, strings.NewReader(newKey().register(t, c)))
			return err
		},
		func(db dbx.DB) error { _, err := with(db).BeginLogin(ctx, "gate@example.org", password); return err },
		func(db dbx.DB) error {
			c, _ := s.BeginLogin(ctx, "gate@example.org", password)
			_, err := with(db).FinishLogin(ctx, c.StaffID, c.State, strings.NewReader(k.sign(t, c)))
			return err
		},
		func(db dbx.DB) error {
			c, _ := s.BeginLogin(ctx, "gate@example.org", password)
			_, err := with(db).Current(ctx, c.StaffID, 1)
			return err
		},
		func(db dbx.DB) error { _, err := with(db).List(ctx); return err },
	}
	for _, op := range ops {
		pgtest.EachFault(t, pool, op, func(int, error) {})
	}
	st, _ := enroll(t, s, "z@example.org", "gate")
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		_, _ = pool.Exec(ctx, `UPDATE staff SET status = 'active' WHERE id = $1`, st.ID)
		return with(db).Disable(ctx, audit.Actor{Kind: "system"}, st.ID, "x")
	}, func(int, error) {})
}
