// Package config reads process configuration from the environment.
// Secrets arrive through systemd credentials or environment files and are
// never compiled into binaries or frontend assets.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"kacheem/backend/internal/sealbox"
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
	Payment         Payment
	Delivery        Delivery
	// PublishDir, if set, lets organizers publish event.json from the
	// dashboard into this buyer web root.
	PublishDir string
}

// Delivery configures ticket issuance and the email and SMS channels.
type Delivery struct {
	// TicketKey seals ticket credentials (32 bytes, base64 in TICKET_KEY).
	// Back it up apart from the database: without it tickets cannot be re-rendered.
	TicketKey []byte
	// GateOrigin is the staff scanner origin printed in every QR code.
	GateOrigin string
	// EmailProvider is "fake" (development outbox) or "smtp".
	EmailProvider string
	SMTPAddr      string
	SMTPUsername  string
	SMTPPassword  string
	EmailFrom     string
	// SMSProvider is "fake" until a Bangladeshi aggregator is contracted.
	SMSProvider string
}

func loadDelivery(getenv func(string) string, errs *[]error) Delivery {
	d := Delivery{
		GateOrigin:    required(getenv, "GATE_ORIGIN", errs),
		EmailProvider: required(getenv, "EMAIL_PROVIDER", errs),
		SMSProvider:   required(getenv, "SMS_PROVIDER", errs),
	}
	if v := required(getenv, "TICKET_KEY", errs); v != "" {
		key, err := sealbox.ParseKey(v)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("TICKET_KEY: %w", err))
		}
		d.TicketKey = key
	}
	switch d.EmailProvider {
	case "", "fake":
	case "smtp":
		d.SMTPAddr = required(getenv, "SMTP_ADDR", errs)
		d.EmailFrom = required(getenv, "EMAIL_FROM", errs)
		d.SMTPUsername, d.SMTPPassword = getenv("SMTP_USERNAME"), getenv("SMTP_PASSWORD")
	default:
		*errs = append(*errs, fmt.Errorf("EMAIL_PROVIDER must be fake or smtp, got %q", d.EmailProvider))
	}
	if d.SMSProvider != "" && d.SMSProvider != "fake" {
		*errs = append(*errs, fmt.Errorf("SMS_PROVIDER must be fake until an SMS aggregator is integrated, got %q", d.SMSProvider))
	}
	d.GateOrigin = strings.TrimRight(d.GateOrigin, "/")
	return d
}

// Payment selects and configures the payment gateway. Provider "fake" is the
// local stand-in for development; "bkash" and "paystation" need the
// merchant credentials.
type Payment struct {
	Provider             string
	BkashBaseURL         string
	BkashUsername        string
	BkashPassword        string
	BkashAppKey          string
	BkashAppSecret       string
	PayStationBaseURL    string
	PayStationMerchantID string
	PayStationPassword   string
}

// DefaultExpiryInterval is how often the worker releases expired holds.
const DefaultExpiryInterval = 5 * time.Second

// Worker is the configuration of cmd/worker.
type Worker struct {
	DatabaseURL    string
	ExpiryInterval time.Duration
	Payment        Payment
	Delivery       Delivery
	// PublicOrigin is the buyer site, used in SMS pickup links.
	PublicOrigin string
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
		ShutdownTimeout: duration(getenv, "SHUTDOWN_TIMEOUT", DefaultShutdownTimeout, &errs),
		Payment:         loadPayment(getenv, &errs),
		Delivery:        loadDelivery(getenv, &errs),
		PublishDir:      getenv("PUBLISH_DIR"),
	}
	return cfg, errors.Join(errs...)
}

// LoadWorker reads the worker configuration.
func LoadWorker(getenv func(string) string) (Worker, error) {
	var errs []error
	cfg := Worker{
		DatabaseURL:    required(getenv, "DATABASE_URL", &errs),
		ExpiryInterval: duration(getenv, "EXPIRY_INTERVAL", DefaultExpiryInterval, &errs),
		Payment:        loadPayment(getenv, &errs),
		Delivery:       loadDelivery(getenv, &errs),
		PublicOrigin:   required(getenv, "PUBLIC_ORIGIN", &errs),
	}
	return cfg, errors.Join(errs...)
}

// LoadDatabase reads only DATABASE_URL, for cmd/migrate.
func LoadDatabase(getenv func(string) string) (string, error) {
	var errs []error
	url := required(getenv, "DATABASE_URL", &errs)
	return url, errors.Join(errs...)
}

func loadPayment(getenv func(string) string, errs *[]error) Payment {
	p := Payment{Provider: required(getenv, "PAYMENT_PROVIDER", errs)}
	switch p.Provider {
	case "", "fake":
	case "bkash":
		p.BkashBaseURL = required(getenv, "BKASH_BASE_URL", errs)
		p.BkashUsername = required(getenv, "BKASH_USERNAME", errs)
		p.BkashPassword = required(getenv, "BKASH_PASSWORD", errs)
		p.BkashAppKey = required(getenv, "BKASH_APP_KEY", errs)
		p.BkashAppSecret = required(getenv, "BKASH_APP_SECRET", errs)
	case "paystation":
		p.PayStationBaseURL = required(getenv, "PAYSTATION_BASE_URL", errs)
		p.PayStationMerchantID = required(getenv, "PAYSTATION_MERCHANT_ID", errs)
		p.PayStationPassword = required(getenv, "PAYSTATION_PASSWORD", errs)
	default:
		*errs = append(*errs, fmt.Errorf("PAYMENT_PROVIDER must be fake, bkash or paystation, got %q", p.Provider))
	}
	return p
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
