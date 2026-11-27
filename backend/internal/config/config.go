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
	PublicListen string
	StaffListen  string
	// PublicOrigin is the exact buyer site origin, e.g. https://tickets.example.org.
	PublicOrigin    string
	ShutdownTimeout time.Duration
}

// DefaultExpiryInterval is how often the worker releases expired holds.
const DefaultExpiryInterval = 5 * time.Second

// Worker is the configuration of cmd/worker and cmd/migrate.
type Worker struct {
	DatabaseURL    string
	ExpiryInterval time.Duration
}

// Publish is the configuration of cmd/publish.
type Publish struct {
	DatabaseURL string
	// Dir is the web root directory that receives event.json.
	Dir string
}

// LoadAPI reads the API configuration, reporting every problem at once.
func LoadAPI(getenv func(string) string) (API, error) {
	var errs []error
	cfg := API{
		DatabaseURL:     required(getenv, "DATABASE_URL", &errs),
		RedisURL:        required(getenv, "REDIS_URL", &errs),
		PublicListen:    required(getenv, "API_PUBLIC_LISTEN", &errs),
		StaffListen:     required(getenv, "API_STAFF_LISTEN", &errs),
		PublicOrigin:    required(getenv, "PUBLIC_ORIGIN", &errs),
		ShutdownTimeout: DefaultShutdownTimeout,
	}
	cfg.ShutdownTimeout = duration(getenv, "SHUTDOWN_TIMEOUT", DefaultShutdownTimeout, &errs)
	return cfg, errors.Join(errs...)
}

// LoadWorker reads the worker and migration configuration.
func LoadWorker(getenv func(string) string) (Worker, error) {
	var errs []error
	cfg := Worker{
		DatabaseURL:    required(getenv, "DATABASE_URL", &errs),
		ExpiryInterval: duration(getenv, "EXPIRY_INTERVAL", DefaultExpiryInterval, &errs),
	}
	return cfg, errors.Join(errs...)
}

// LoadPublish reads the publish job configuration.
func LoadPublish(getenv func(string) string) (Publish, error) {
	var errs []error
	cfg := Publish{
		DatabaseURL: required(getenv, "DATABASE_URL", &errs),
		Dir:         required(getenv, "PUBLISH_DIR", &errs),
	}
	return cfg, errors.Join(errs...)
}

// duration reads an optional positive duration.
func duration(getenv func(string) string, name string, def time.Duration, errs *[]error) time.Duration {
	v := getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive duration, got %q", name, v))
	}
	return d
}

func required(getenv func(string) string, name string, errs *[]error) string {
	v := getenv(name)
	if v == "" {
		*errs = append(*errs, fmt.Errorf("%s is required", name))
	}
	return v
}
