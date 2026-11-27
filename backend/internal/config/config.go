// Package config reads process configuration from the environment.
// Secrets arrive through systemd credentials or environment files and are
// never compiled into binaries or frontend assets.
package config

import (
	"errors"
	"fmt"
	"time"
)

// DefaultShutdownTimeout bounds graceful shutdown when SHUTDOWN_TIMEOUT is unset.
const DefaultShutdownTimeout = 20 * time.Second

// API is the configuration of cmd/api.
type API struct {
	DatabaseURL string
	RedisURL    string
	// PublicListen and StaffListen are "unix:/path" or "tcp:host:port".
	PublicListen    string
	StaffListen     string
	ShutdownTimeout time.Duration
}

// Worker is the configuration of cmd/worker and cmd/migrate.
type Worker struct {
	DatabaseURL string
}

// LoadAPI reads the API configuration, reporting every problem at once.
func LoadAPI(getenv func(string) string) (API, error) {
	var errs []error
	cfg := API{
		DatabaseURL:     required(getenv, "DATABASE_URL", &errs),
		RedisURL:        required(getenv, "REDIS_URL", &errs),
		PublicListen:    required(getenv, "API_PUBLIC_LISTEN", &errs),
		StaffListen:     required(getenv, "API_STAFF_LISTEN", &errs),
		ShutdownTimeout: DefaultShutdownTimeout,
	}
	if v := getenv("SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration, got %q", v))
		}
		cfg.ShutdownTimeout = d
	}
	return cfg, errors.Join(errs...)
}

// LoadWorker reads the worker and migration configuration.
func LoadWorker(getenv func(string) string) (Worker, error) {
	var errs []error
	cfg := Worker{DatabaseURL: required(getenv, "DATABASE_URL", &errs)}
	return cfg, errors.Join(errs...)
}

func required(getenv func(string) string, name string, errs *[]error) string {
	v := getenv(name)
	if v == "" {
		*errs = append(*errs, fmt.Errorf("%s is required", name))
	}
	return v
}
