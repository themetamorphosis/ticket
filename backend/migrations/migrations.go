// Package migrations embeds the Goose SQL migrations so binaries carry their
// own schema. The files also work with the goose CLI:
//
//	goose -dir backend/migrations postgres "$DATABASE_URL" up
package migrations

import (
	"database/sql"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var embedded embed.FS

// Files is the embedded migration set.
var Files fs.FS = embedded

// NewProvider returns a Goose provider for fsys against a PostgreSQL db.
func NewProvider(db *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, db, fsys)
}
