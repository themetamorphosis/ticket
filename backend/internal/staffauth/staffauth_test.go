package staffauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Pool(t)
	return New(pool), pool
}

const password = "correct horse battery staple"

// enroll invites someone and accepts their invitation.
func enroll(t *testing.T, s *Service, email, role string) Staff {
	t.Helper()
	token, err := s.Invite(ctx, audit.Actor{Kind: "system"}, email, "Test "+role, role)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.AcceptInvite(ctx, token, password)
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	return st
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

func TestInviteAcceptAndSignIn(t *testing.T) {
	s, pool := newService(t)
	st := enroll(t, s, " Gate@Example.org ", "gate")
	if st.Status != "active" || st.Email != "gate@example.org" || st.Role != "gate" || st.Epoch != 1 {
		t.Fatalf("staff %+v", st)
	}
	signedIn, err := s.SignIn(ctx, "GATE@example.org", password)
	if err != nil || signedIn != st {
		t.Fatalf("SignIn %+v %v", signedIn, err)
	}
	cur, err := s.Current(ctx, st.ID, 1)
	if err != nil || cur.Name != "Test gate" {
		t.Fatalf("Current %+v %v", cur, err)
	}
	var audits int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE action IN ('staff.invite', 'staff.activate', 'staff.sign_in')`).Scan(&audits)
	if audits != 3 {
		t.Fatalf("audits %d", audits)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List %v %v", list, err)
	}
}

func TestSignInFailuresAreIndistinguishable(t *testing.T) {
	s, _ := newService(t)
	enroll(t, s, "gate@example.org", "gate")
	if _, err := s.Invite(ctx, audit.Actor{Kind: "system"}, "new@example.org", "New", "support"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"unknown email":   {"nobody@example.org", password},
		"wrong password":  {"gate@example.org", "wrong password!!"},
		"invited account": {"new@example.org", password},
	} {
		if st, err := s.SignIn(ctx, c[0], c[1]); !errors.Is(err, ErrLogin) || st != (Staff{}) {
			t.Errorf("%s: %+v %v", name, st, err)
		}
	}
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
	if _, err := s.AcceptInvite(ctx, token, "short"); !errors.Is(err, ErrPassword) {
		t.Fatalf("short password: %v", err)
	}
	if _, err := s.AcceptInvite(ctx, "not-a-token", password); !errors.Is(err, ErrInvite) {
		t.Fatalf("bad token: %v", err)
	}
	if _, err := s.AcceptInvite(ctx, token, password); err != nil {
		t.Fatal(err)
	}
	// Used links do not work again (and cannot reset the password), and active
	// accounts are not re-invited.
	if _, err := s.AcceptInvite(ctx, token, "another long password"); !errors.Is(err, ErrInvite) {
		t.Fatalf("reused: %v", err)
	}
	if _, err := s.SignIn(ctx, "a@example.org", password); err != nil {
		t.Fatalf("password changed by a used link: %v", err)
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
	if _, err := s.AcceptInvite(ctx, expired, password); !errors.Is(err, ErrInvite) {
		t.Fatalf("expired: %v", err)
	}
}

func TestAnInvitationIsAcceptedOnlyOnce(t *testing.T) {
	s, _ := newService(t)
	token, _ := s.Invite(ctx, audit.Actor{Kind: "system"}, "race@example.org", "Race", "gate")
	errs := make(chan error)
	for range 4 {
		go func() {
			_, err := s.AcceptInvite(ctx, token, password)
			errs <- err
		}()
	}
	accepted := 0
	for range 4 {
		switch err := <-errs; {
		case err == nil:
			accepted++
		case !errors.Is(err, ErrInvite):
			t.Errorf("err = %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d times", accepted)
	}
}

func TestDisableRevokesEverySession(t *testing.T) {
	s, _ := newService(t)
	st := enroll(t, s, "gate@example.org", "gate")
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
	if _, err := s.SignIn(ctx, "gate@example.org", password); !errors.Is(err, ErrLogin) {
		t.Fatalf("disabled sign-in: %v", err)
	}
	if err := s.Disable(ctx, audit.Actor{Kind: "system"}, "00000000-0000-0000-0000-000000000000", "x"); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown disable: %v", err)
	}
}

func TestFaults(t *testing.T) {
	s, pool := newService(t)
	st := enroll(t, s, "gate@example.org", "gate")
	with := func(db dbx.DB) *Service { c := *s; c.DB = db; return &c }
	n := 0
	ops := []func(db dbx.DB) error{
		func(db dbx.DB) error {
			n++
			_, err := with(db).Invite(ctx, audit.Actor{Kind: "system"}, strings.Repeat("x", n)+"@example.org", "X", "gate")
			return err
		},
		func(db dbx.DB) error {
			n++
			token, _ := s.Invite(ctx, audit.Actor{Kind: "system"}, strings.Repeat("y", n)+"@example.org", "Y", "gate")
			_, err := with(db).AcceptInvite(ctx, token, password)
			return err
		},
		func(db dbx.DB) error { _, err := with(db).SignIn(ctx, "gate@example.org", password); return err },
		func(db dbx.DB) error { _, err := with(db).Current(ctx, st.ID, 1); return err },
		func(db dbx.DB) error { _, err := with(db).List(ctx); return err },
	}
	for _, op := range ops {
		pgtest.EachFault(t, pool, op, func(int, error) {})
	}
	other := enroll(t, s, "z@example.org", "gate")
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		_, _ = pool.Exec(ctx, `UPDATE staff SET status = 'active' WHERE id = $1`, other.ID)
		return with(db).Disable(ctx, audit.Actor{Kind: "system"}, other.ID, "x")
	}, func(int, error) {})
}
