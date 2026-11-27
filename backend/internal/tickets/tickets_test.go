package tickets

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makiuchi-d/gozxing"
	gozxingqr "github.com/makiuchi-d/gozxing/qrcode"
	"github.com/skip2/go-qrcode"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/sealbox"
)

var ctx = context.Background()

func TestCredentials(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		c := NewCredential()
		if len(c) != 22 || seen[c] || strings.ContainsAny(c, "+/=") {
			t.Fatalf("bad or repeated credential %q", c)
		}
		seen[c] = true
	}
	if len(Digest("x")) != 32 || bytes.Equal(Digest("x"), Digest("y")) {
		t.Fatal("digest")
	}
}

func TestFallbackCodes(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}-[0-9A-HJKMNP-TV-Z*~$=U]$`)
	for range 1000 {
		code := NewFallbackCode()
		if !shape.MatchString(code) {
			t.Fatalf("shape %q", code)
		}
		norm, ok := NormalizeFallback(code)
		if !ok || len(norm) != 17 {
			t.Fatalf("own code rejected: %q", code)
		}
		// Staff typing: lower case, spaces, no hyphens.
		typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
		if again, ok := NormalizeFallback(typed); !ok || again != norm {
			t.Fatalf("typed %q -> %q %v", typed, again, ok)
		}
	}
	// O, I and L are read as 0, 1 and 1.
	code := "0000-1111-0000-1111-"
	code += string(check(strings.ReplaceAll(code[:19], "-", "")))
	if norm, ok := NormalizeFallback(strings.NewReplacer("0", "O", "1", "l").Replace(code)); !ok || norm != strings.ReplaceAll(code, "-", "") {
		t.Fatalf("look-alikes: %q %v", norm, ok)
	}
	good := NewFallbackCode()
	typo := []byte(good)
	typo[0] = map[bool]byte{true: 'A', false: 'B'}[typo[0] != 'A']
	for _, bad := range []string{"", "ABCD", string(typo), good + "X", strings.Replace(good, good[:1], "#", 1)} {
		if _, ok := NormalizeFallback(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func issuer(t *testing.T) *Issuer {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	box, _ := sealbox.New(k)
	return &Issuer{Box: box}
}

func paidOrder(t *testing.T, pool *pgxpool.Pool, qty int) string {
	t.Helper()
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	o, _, err := orders.Reserve(ctx, pool, orders.Request{EventSlug: "live", BuyerID: "b", IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: qty, Name: "A Buyer", Email: "a@example.com", Phone: "+8801711000000", Age: 30}})
	if err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func TestIssueCreatesOneTicketPerPlaceOnce(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := paidOrder(t, pool, 3)
	iss := issuer(t)
	for range 2 {
		if err := iss.Issue(ctx, pool, oid); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := pool.Query(ctx, `SELECT ordinal, credential_sealed, credential_digest, fallback_digest FROM ticket ORDER BY ordinal`)
	var n int
	for rows.Next() {
		var ord int
		var sealed, cd, fd []byte
		_ = rows.Scan(&ord, &sealed, &cd, &fd)
		n++
		s, err := iss.Open(sealed)
		if err != nil || ord != n || !bytes.Equal(Digest(s.Credential), cd) {
			t.Fatalf("ticket %d: %+v %v", ord, s, err)
		}
		norm, _ := NormalizeFallback(s.Fallback)
		if !bytes.Equal(Digest(norm), fd) {
			t.Fatal("fallback digest")
		}
	}
	var jobs int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_job WHERE kind = 'render_tickets' AND order_id = $1`, oid).Scan(&jobs)
	if n != 3 || jobs != 1 {
		t.Fatalf("%d tickets, %d jobs", n, jobs)
	}
	list, err := ForOrder(ctx, pool, oid)
	if err != nil || len(list) != 3 || list[2].Ordinal != 3 || list[2].Of != 3 || list[0].Ready || list[0].Status != "valid" {
		t.Fatalf("ForOrder %+v %v", list, err)
	}
	if _, err := issuer(t).Open([]byte("garbage")); !errors.Is(err, ErrSealed) {
		t.Fatal("garbage opened")
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error { return iss.Issue(ctx, db, oid) }, func(int, error) {})
	pgtest.EachFault(t, pool, func(db dbx.DB) error { _, err := ForOrder(ctx, db, oid); return err }, func(int, error) {})
}

func doc() Doc {
	return Doc{Organizer: "Kacheem Comms", EventTitle: "Kacheem Live", StartsAt: time.Date(2027, 2, 14, 13, 30, 0, 0, time.UTC),
		TimeZone: "Asia/Dhaka", Venue: "Test Hall, Dhaka", TicketType: "General admission", Ordinal: 2, Of: 3,
		OrderRef: "AB12C-DE34F", FallbackCode: "7K2Q-HM9X-4DAB-T0RE-Q", QRContent: "https://gate.example.org/scan#" + NewCredential()}
}

func TestRenderProducesASinglePagePDF(t *testing.T) {
	d := doc()
	pdf, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("/Subtype /Image")) ||
		bytes.Count(pdf, []byte("/Type /Page\n")) != 1 || len(pdf) > 300_000 {
		t.Fatalf("pdf %d bytes", len(pdf))
	}
	if bytes.Contains(pdf, []byte("a@example.com")) {
		t.Fatal("contact details on the ticket")
	}
	// Every line of text is drawn: organizer, title, date, venue, ticket
	// line, three centered lines and the two-line footer at least.
	if n := textOps(t, pdf); n < 10 {
		t.Fatalf("only %d text drawing operations", n)
	}
	long := d
	long.EventTitle = strings.Repeat("A very long concert title ", 6)
	longPDF, _ := Render(long)
	if textOps(t, longPDF) <= textOps(t, pdf) {
		t.Fatal("long title did not wrap onto more lines")
	}
	if d.FileName() != "ticket-2-of-3.pdf" {
		t.Fatal(d.FileName())
	}
}

// The QR placed on the ticket decodes back to the exact staff URL.
func TestQRIsScannable(t *testing.T) {
	content := doc().QRContent
	q, _ := qrcode.New(content, qrcode.Medium)
	img := q.Image(1024)
	bmp, _ := gozxing.NewBinaryBitmapFromImage(img)
	res, err := gozxingqr.NewQRCodeReader().Decode(bmp, nil)
	if err != nil || res.GetText() != content {
		t.Fatalf("decoded %v %v", res, err)
	}
	// Quiet zone: the border is white.
	if r, _, _, _ := img.At(2, 2).RGBA(); r != 0xffff {
		t.Fatal("no quiet zone")
	}
}

func TestRenderErrors(t *testing.T) {
	bad := doc()
	bad.TimeZone = "Mars/Olympus"
	if _, err := Render(bad); err == nil {
		t.Error("bad zone rendered")
	}
	huge := doc()
	huge.QRContent = strings.Repeat("x", 5000)
	if _, err := Render(huge); err == nil {
		t.Error("oversized QR rendered")
	}
	orig := fontRegular
	defer func() { fontRegular = orig }()
	fontRegular = []byte("not a font")
	if _, err := Render(doc()); err == nil || !strings.Contains(err.Error(), "render") {
		t.Errorf("broken font: %v", err)
	}
}

func TestDocument(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := paidOrder(t, pool, 2)
	_ = issuer(t).Issue(ctx, pool, oid)
	var id string
	_ = pool.QueryRow(ctx, `SELECT id FROM ticket WHERE ordinal = 2`).Scan(&id)
	if _, err := Document(ctx, pool, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unrendered: %v", err)
	}
	_, _ = pool.Exec(ctx, `INSERT INTO ticket_document (ticket_id, version, pdf, size_bytes, sha256)
		VALUES ($1, 1, '%PDF-x', 6, decode(repeat('00', 32), 'hex'))`, id)
	f, err := Document(ctx, pool, id)
	if err != nil || f.OrderID != oid || f.BuyerID != "b" || f.Name() != "ticket-2-of-2.pdf" || string(f.PDF) != "%PDF-x" {
		t.Fatalf("document %+v %v", f, err)
	}
	for _, bad := range []string{"nope", "00000000-0000-0000-0000-000000000000"} {
		if _, err := Document(ctx, pool, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error { _, err := Document(ctx, db, id); return err }, func(int, error) {})
}

func TestRedeem(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := paidOrder(t, pool, 1)
	for secret, sql := range map[string]string{
		"valid":   `now() + interval '1 day', NULL`,
		"expired": `now() - interval '1 second', NULL`,
		"revoked": `now() + interval '1 day', now()`,
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO download_grant (order_id, secret_digest, expires_at, revoked_at)
			VALUES ($1, $2, `+sql+`)`, oid, Digest(secret)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := Redeem(ctx, pool, "valid"); err != nil || got != oid {
		t.Fatalf("valid: %s %v", got, err)
	}
	for _, s := range []string{"expired", "revoked", "unknown"} {
		if _, err := Redeem(ctx, pool, s); !errors.Is(err, ErrGrantInvalid) {
			t.Errorf("%s: %v", s, err)
		}
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error { _, err := Redeem(ctx, db, "valid"); return err }, func(int, error) {})
}

func TestIssueInTx(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := paidOrder(t, pool, 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := issuer(t).IssueInTx(ctx, tx, oid); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM ticket WHERE order_id = $1`, oid).Scan(&n)
	if n != 2 {
		t.Fatalf("%d tickets", n)
	}
}

// textOps inflates the PDF's content streams and counts text-show operators.
func textOps(t *testing.T, pdf []byte) int {
	t.Helper()
	n := 0
	for _, part := range bytes.Split(pdf, []byte(">>\nstream\n"))[1:] {
		end := bytes.Index(part, []byte("endstream"))
		if end < 0 {
			continue
		}
		r, err := zlib.NewReader(bytes.NewReader(part[:end]))
		if err != nil {
			continue // not a Flate stream (fonts, images)
		}
		content, _ := io.ReadAll(r)
		n += bytes.Count(content, []byte(" Tj")) + bytes.Count(content, []byte(" TJ"))
	}
	return n
}
