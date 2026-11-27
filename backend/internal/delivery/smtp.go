package delivery

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// SMTP sends through an SMTP relay. It upgrades to TLS when the server offers
// STARTTLS and authenticates when a username is set (Go's PlainAuth refuses
// to send credentials without TLS except to localhost).
type SMTP struct {
	Addr     string // host:port
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

// Send delivers e and returns its Message-ID.
func (s SMTP) Send(ctx context.Context, e Email) (string, error) {
	host, _, _ := net.SplitHostPort(s.Addr)
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return "", fmt.Errorf("smtp: dial: %w", err)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return "", fmt.Errorf("smtp: greeting: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return "", fmt.Errorf("smtp: starttls: %w", err)
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return "", fmt.Errorf("smtp: auth: %w", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return "", fmt.Errorf("smtp: mail from: %w", err)
	}
	if err := c.Rcpt(e.To); err != nil {
		return "", fmt.Errorf("smtp: rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return "", fmt.Errorf("smtp: data: %w", err)
	}
	_, _ = w.Write(BuildMIME(s.From, e))
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("smtp: message rejected: %w", err)
	}
	_ = c.Quit()
	return e.MessageID, nil
}

// BuildMIME renders e as a multipart/mixed message with base64 parts.
func BuildMIME(from string, e Email) []byte {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	boundary := "kc-" + hex.EncodeToString(b)

	var buf bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	header("From", from)
	header("To", e.To)
	header("Subject", mime.QEncoding.Encode("utf-8", e.Subject))
	header("Message-ID", e.MessageID)
	header("Date", time.Now().UTC().Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	header("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	buf.WriteString("\r\n")

	part := func(h textproto.MIMEHeader, data []byte) {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		for _, k := range []string{"Content-Type", "Content-Disposition"} {
			if v := h.Get(k); v != "" {
				header(k, v)
			}
		}
		header("Content-Transfer-Encoding", "base64")
		buf.WriteString("\r\n")
		enc := base64.StdEncoding.EncodeToString(data)
		for len(enc) > 76 {
			buf.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		buf.WriteString(enc + "\r\n")
	}
	part(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}}, []byte(e.Text))
	for _, a := range e.Attachments {
		name := strings.ReplaceAll(a.Name, `"`, "")
		part(textproto.MIMEHeader{
			"Content-Type":        {a.ContentType + `; name="` + name + `"`},
			"Content-Disposition": {`attachment; filename="` + name + `"`},
		}, a.Data)
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes()
}
