// Package redisx opens the Redis client used for the waiting room, rate
// limits and cached display counters. Redis never holds inventory, orders,
// payments, tickets or admissions; PostgreSQL is the only source of truth.
package redisx

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Open parses url without connecting: the API must start, and degrade as the
// plan defines, while Redis is unavailable. Readiness reports its state.
// Production will switch to the Sentinel-aware failover client.
func Open(url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return redis.NewClient(opts), nil
}

// Ping adapts a client to a readiness check.
func Ping(c *redis.Client) func(context.Context) error {
	return func(ctx context.Context) error { return c.Ping(ctx).Err() }
}
