package delivery

import (
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"kacheem/backend/internal/dbx"
)

var outboxPage = template.Must(template.New("outbox").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Development outbox</title>
<style>body{font-family:system-ui,sans-serif;max-width:48rem;margin:2rem auto;padding:0 1rem}
article{border:1px solid #ccc;border-radius:8px;padding:.75rem 1rem;margin:1rem 0}pre{white-space:pre-wrap}
small{color:#666}</style></head><body>
<h1>Development outbox</h1>
<p>Messages the development email and SMS channels would have sent. Nothing here left this machine.</p>
{{range .}}<article>
<small>{{.When}} · {{.Channel}} to {{.Recipient}}</small>
{{if .Subject}}<h2>{{.Subject}}</h2>{{end}}
<pre>{{.Body}}</pre>
{{range .Attachments}}<p><a href="/dev/outbox/ticket/{{.TicketID}}/{{.Version}}">{{.Name}}</a></p>{{end}}
</article>{{else}}<p>No messages yet.</p>{{end}}
</body></html>`))

type devRow struct {
	Channel   string    `db:"channel"`
	Recipient string    `db:"recipient"`
	Subject   string    `db:"subject"`
	Body      string    `db:"body"`
	Atts      []byte    `db:"attachments"`
	Created   time.Time `db:"created_at"`
}

type devView struct {
	devRow
	When        string
	Attachments []devAttachment
}

// DevOutbox serves /dev/outbox (the last 50 development messages) and
// /dev/outbox/ticket/{id}/{version} (an attached PDF). Mount it only with
// the development channels: it shows messages to any visitor.
func DevOutbox(db dbx.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dev/outbox", func(w http.ResponseWriter, r *http.Request) {
		rows, err := dbx.All[devRow](r.Context(), db, `SELECT channel, recipient, subject, body, attachments::text::bytea AS attachments, created_at
			FROM dev_message ORDER BY id DESC LIMIT 50`)
		if err != nil {
			http.Error(w, "outbox unavailable", http.StatusInternalServerError)
			return
		}
		views := make([]devView, len(rows))
		for i, row := range rows {
			views[i] = devView{devRow: row, When: row.Created.UTC().Format(time.RFC3339)}
			_ = json.Unmarshal(row.Atts, &views[i].Attachments)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = outboxPage.Execute(w, views)
	})
	mux.HandleFunc("GET /dev/outbox/ticket/{id}/{version}", func(w http.ResponseWriter, r *http.Request) {
		var pdf []byte
		err := db.QueryRow(r.Context(), `SELECT pdf FROM ticket_document WHERE ticket_id::text = $1 AND version::text = $2`,
			r.PathValue("id"), r.PathValue("version")).Scan(&pdf)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	})
	return mux
}
