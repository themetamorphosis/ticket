package delivery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"

	"kacheem/backend/internal/config"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

// smtpServer is a minimal SMTP server that can refuse at one command.
type smtpServer struct {
	ln       net.Listener
	failAt   string // "AUTH", "MAIL", "RCPT", "DATA", "BODY", "GREETING"
	starttls bool
	got      chan []byte
	auth     chan string
}

func newSMTP(t *testing.T, failAt string, starttls bool) *smtpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpServer{ln: ln, failAt: failAt, starttls: starttls, got: make(chan []byte, 1), auth: make(chan string, 1)}
	t.Cleanup(func() { ln.Close() })
	go s.serve()
	return s
}

func (s *smtpServer) serve() {
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(l string) { _, _ = conn.Write([]byte(l + "\r\n")) }
	if s.failAt == "GREETING" {
		say("554 go away")
		return
	}
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.Fields(line + " x")[0])
		switch {
		case cmd == "EHLO":
			if s.starttls {
				say("250-fake")
				say("250-STARTTLS")
			} else {
				say("250-fake")
			}
			say("250 AUTH PLAIN")
		case cmd == "STARTTLS":
			say("454 TLS not available")
		case cmd == "AUTH":
			s.auth <- strings.TrimSpace(line)
			if s.failAt == "AUTH" {
				say("535 bad credentials")
			} else {
				say("235 ok")
			}
		case cmd == "MAIL" || cmd == "RCPT":
			if s.failAt == cmd {
				say("550 no")
			} else {
				say("250 ok")
			}
		case cmd == "DATA":
			if s.failAt == "DATA" {
				say("554 no data")
				continue
			}
			say("354 go ahead")
			var msg bytes.Buffer
			for {
				l, _ := r.ReadString('\n')
				if l == ".\r\n" {
					break
				}
				msg.WriteString(l)
			}
			s.got <- msg.Bytes()
			if s.failAt == "BODY" {
				say("554 rejected")
			} else {
				say("250 queued")
			}
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func email() Email {
	return Email{To: "nusrat@example.com", Subject: "Your 2 tickets for Kacheem Live — ঢাকা", Text: "Hello\nTwo tickets attached.",
		MessageID: "<AB12C-DE34F.1@tickets.example.org>",
		Attachments: []Attachment{
			{Name: "ticket-1-of-2.pdf", ContentType: "application/pdf", Data: bytes.Repeat([]byte("%PDF-1"), 40)},
			{Name: "ticket-2-of-2.pdf", ContentType: "application/pdf", Data: []byte("%PDF-2")},
		}}
}

func TestSMTPDeliversAMultipartMessage(t *testing.T) {
	srv := newSMTP(t, "", false)
	m := SMTP{Addr: srv.ln.Addr().String(), Username: "relay", Password: "secret", From: "tickets@example.org", Timeout: 5 * time.Second}
	id, err := m.Send(ctx, email())
	if err != nil || id != "<AB12C-DE34F.1@tickets.example.org>" {
		t.Fatalf("send %q %v", id, err)
	}
	if a := <-srv.auth; !strings.HasPrefix(a, "AUTH PLAIN ") {
		t.Fatalf("auth %q", a)
	}
	raw := <-srv.got
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subject, _ := dec.DecodeHeader(msg.Header.Get("Subject"))
	if subject != email().Subject || msg.Header.Get("Message-ID") != email().MessageID || msg.Header.Get("To") != "nusrat@example.com" {
		t.Fatalf("headers %v", msg.Header)
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var names []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		body, _ := io.ReadAll(p)
		if p.FileName() == "" {
			continue
		}
		names = append(names, p.FileName())
		_ = body
	}
	if strings.Join(names, ",") != "ticket-1-of-2.pdf,ticket-2-of-2.pdf" {
		t.Fatalf("attachments %v", names)
	}
	// Long base64 lines are wrapped.
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatal("line too long")
		}
	}
}

func TestSMTPFailures(t *testing.T) {
	for _, failAt := range []string{"GREETING", "AUTH", "MAIL", "RCPT", "DATA", "BODY", "STARTTLS"} {
		srv := newSMTP(t, failAt, failAt == "STARTTLS")
		m := SMTP{Addr: srv.ln.Addr().String(), Username: "relay", Password: "x", From: "tickets@example.org", Timeout: 5 * time.Second}
		if failAt == "MAIL" {
			m.Username = "" // also covers sending without authentication
		}
		if _, err := m.Send(ctx, email()); err == nil {
			t.Errorf("%s: no error", failAt)
		}
	}
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := closed.Addr().String()
	closed.Close()
	if _, err := (SMTP{Addr: addr, Timeout: time.Second}).Send(ctx, email()); err == nil || !strings.Contains(err.Error(), "dial") {
		t.Fatalf("dial: %v", err)
	}
}

func TestDevChannelsRecordMessages(t *testing.T) {
	pool := pgtest.Pool(t)
	id, err := DevMailer{DB: pool}.Send(ctx, Email{To: "a@example.com", Subject: "S", Text: "T",
		Attachments: []Attachment{{Name: "ticket-1-of-1.pdf", TicketID: "t1", Version: 1}}})
	if err != nil || !strings.HasPrefix(id, "dev-") {
		t.Fatalf("mail %s %v", id, err)
	}
	if _, err := (DevSMS{DB: pool}).Send(ctx, "+8801711000000", "Pick up your tickets"); err != nil {
		t.Fatal(err)
	}
	var atts []byte
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*), max(attachments::text) FROM dev_message`).Scan(&n, &atts)
	var parsed []map[string]any
	_ = json.Unmarshal(atts, &parsed)
	if n != 2 || len(parsed) != 1 || parsed[0]["name"] != "ticket-1-of-1.pdf" {
		t.Fatalf("%d messages, attachments %s", n, atts)
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error { _, err := (DevSMS{DB: db}).Send(ctx, "x", "y"); return err }, func(int, error) {})
}

func TestDevOutboxPage(t *testing.T) {
	pool := pgtest.Pool(t)
	h := DevOutbox(pool)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/dev/outbox"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "No messages yet") {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body.String())
	}
	_, _ = DevMailer{DB: pool}.Send(ctx, Email{To: "a@example.com", Subject: "Your <tickets>", Text: "Body",
		Attachments: []Attachment{{Name: "ticket-1-of-1.pdf", TicketID: "00000000-0000-0000-0000-000000000001", Version: 1}}})
	_, _ = DevSMS{DB: pool}.Send(ctx, "+8801711000000", "Download: https://tickets.test/pickup/#abc")
	rec := get("/dev/outbox")
	body := rec.Body.String()
	if !strings.Contains(body, "Your &lt;tickets&gt;") || !strings.Contains(body, `/dev/outbox/ticket/00000000-0000-0000-0000-000000000001/1`) ||
		!strings.Contains(body, "pickup/#abc") || strings.Index(body, "pickup") > strings.Index(body, "Your &lt;") {
		t.Fatalf("page %s", body)
	}
	if rec := get("/dev/outbox/ticket/nope/1"); rec.Code != 404 {
		t.Fatalf("missing pdf %d", rec.Code)
	}
	broken := DevOutbox(pgtest.NewFaulty(pool, 1))
	rec = httptest.NewRecorder()
	broken.ServeHTTP(rec, httptest.NewRequest("GET", "/dev/outbox", nil))
	if rec.Code != 500 {
		t.Fatalf("db failure %d", rec.Code)
	}
}

func TestDevOutboxServesTicketPDF(t *testing.T) {
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	var tid string
	err := pool.QueryRow(ctx, `
		WITH o AS (
			INSERT INTO ticket_order (public_ref, event_id, buyer_id, idempotency_key, request_digest, purchaser_name, email, phone,
				age, currency, total_minor, expires_at, status, closed_at)
			VALUES ('ABCDE-FGHJK', $1, 'b', 'key-12345', decode(repeat('00', 32), 'hex'), 'A', 'a@example.com', '+8801711000000',
				30, 'BDT', 1, now(), 'paid', now()) RETURNING id
		), t AS (
			INSERT INTO ticket (order_id, ticket_type_id, ordinal, credential_digest, fallback_digest, credential_sealed)
			SELECT o.id, $2, 1, decode(repeat('01', 32), 'hex'), decode(repeat('02', 32), 'hex'), '\x00' FROM o RETURNING id
		)
		INSERT INTO ticket_document (ticket_id, version, pdf, size_bytes, sha256)
		SELECT t.id, 1, '%PDF-x', 6, decode(repeat('00', 32), 'hex') FROM t RETURNING ticket_id::text`, ids.EventID, ids.TicketTypeID).Scan(&tid)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	DevOutbox(pool).ServeHTTP(rec, httptest.NewRequest("GET", "/dev/outbox/ticket/"+tid+"/1", nil))
	if rec.Code != 200 || rec.Body.String() != "%PDF-x" || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestConstructors(t *testing.T) {
	smtpCfg := config.Delivery{EmailProvider: "smtp", SMTPAddr: "smtp.example.org:587", EmailFrom: "t@example.org", SMSProvider: "fake"}
	if m, ok := NewMailer(smtpCfg, nil).(SMTP); !ok || m.Addr != "smtp.example.org:587" || m.Timeout == 0 {
		t.Fatal("smtp mailer")
	}
	if _, ok := NewMailer(config.Delivery{EmailProvider: "fake"}, nil).(DevMailer); !ok {
		t.Fatal("dev mailer")
	}
	if _, ok := NewSMS(smtpCfg, nil).(DevSMS); !ok {
		t.Fatal("dev sms")
	}
	if !UsesDevChannels(smtpCfg) || !UsesDevChannels(config.Delivery{EmailProvider: "fake"}) ||
		UsesDevChannels(config.Delivery{EmailProvider: "smtp", SMSProvider: "carrier"}) {
		t.Fatal("UsesDevChannels")
	}
}
