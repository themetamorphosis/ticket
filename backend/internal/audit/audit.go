// Package audit appends to the append-only audit_event table.
package audit

import (
	"context"
	"encoding/json"

	"kacheem/backend/internal/dbx"
)

// Actor is who did something: a staff member, or the system.
type Actor struct {
	Kind string // "staff" or "system"
	ID   string // staff ID; empty for the system
}

// Staff returns the actor for a staff member.
func Staff(id string) Actor { return Actor{Kind: "staff", ID: id} }

// Entry is one audited action.
type Entry struct {
	At         string `db:"at" json:"at"`
	Actor      string `db:"actor" json:"actor"`
	Action     string `db:"action" json:"action"`
	ObjectKind string `db:"object_kind" json:"object_kind"`
	ObjectID   string `db:"object_id" json:"object_id"`
	Reason     string `db:"reason" json:"reason"`
}

// Record appends one entry. Call it in the transaction that makes the change.
func Record(ctx context.Context, db dbx.DB, a Actor, action, objectKind, objectID, reason string, detail map[string]any) error {
	b, _ := json.Marshal(detail)
	if detail == nil {
		b = []byte("{}")
	}
	var actorID *string
	if a.ID != "" {
		actorID = &a.ID
	}
	_, err := db.Exec(ctx, `INSERT INTO audit_event (actor_kind, actor_id, action, object_kind, object_id, reason, detail)
		VALUES ($1, $2, $3, $4, $5, nullif($6, ''), $7::jsonb)`, a.Kind, actorID, action, objectKind, objectID, reason, string(b))
	return err
}

// Recent lists the latest entries, newest first, naming staff actors.
func Recent(ctx context.Context, db dbx.DB, limit int) ([]Entry, error) {
	return dbx.All[Entry](ctx, db, `
		SELECT to_char(e.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS at,
			coalesce(s.name || ' <' || s.email || '>', e.actor_kind) AS actor,
			e.action, e.object_kind, e.object_id, coalesce(e.reason, '') AS reason
		FROM audit_event e LEFT JOIN staff s ON e.actor_kind = 'staff' AND s.id::text = e.actor_id
		ORDER BY e.id DESC LIMIT $1`, limit)
}
