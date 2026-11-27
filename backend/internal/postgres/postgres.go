// Package postgres opens the pgx connection pool shared by the API and workers.
// Production connects through PgBouncer in transaction-pooling mode, so the
// pool never relies on server-side prepared statements.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newPool is a seam for testing pool construction failures.
var newPool = pgxpool.NewWithConfig

// Open parses url (pool size via pool_max_conns), connects and pings once so
// a misconfigured process fails at start rather than on its first request.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	// QueryExecModeExec is compatible with PgBouncer transaction pooling.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	pool, err := newPool(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}
