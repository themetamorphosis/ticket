package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// testKey is a valid base64 TICKET_KEY (32 bytes).
const testKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

func deliveryEnv(m map[string]string) map[string]string {
	for k, v := range map[string]string{"TICKET_KEY": testKey, "GATE_ORIGIN": "https://gate.example.org/",
		"EMAIL_PROVIDER": "fake", "SMS_PROVIDER": "fake"} {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	return m
}

// workerEnv is a complete worker environment with overrides.
func workerEnv(over map[string]string) map[string]string {
	m := deliveryEnv(map[string]string{"DATABASE_URL": "x", "PUBLIC_ORIGIN": "https://tickets.example.org", "PAYMENT_PROVIDER": "fake"})
	for k, v := range over {
		m[k] = v
	}
	return m
}

func fullAPIEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":       "postgres://db/tickets",
		"REDIS_URL":          "redis://redis:6379/0",
		"API_PUBLIC_LISTEN":  "unix:/run/kacheem/public.sock",
		"API_STAFF_LISTEN":   "unix:/run/kacheem/staff.sock",
		"PUBLIC_ORIGIN":      "https://tickets.example.org",
		"PAYMENT_PROVIDER":   "fake",
		"SHUTDOWN_TIMEOUT":   "7s",
		"UNRELATED_VARIABLE": "ignored",
		"TICKET_KEY":         testKey,
		"GATE_ORIGIN":        "https://gate.example.org/",
		"EMAIL_PROVIDER":     "fake",
		"SMS_PROVIDER":       "fake",
	}
}

func key() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestLoadAPIReadsEveryField(t *testing.T) {
	cfg, err := LoadAPI(env(fullAPIEnv()))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	want := API{
		DatabaseURL:     "postgres://db/tickets",
		RedisURL:        "redis://redis:6379/0",
		PublicListen:    "unix:/run/kacheem/public.sock",
		StaffListen:     "unix:/run/kacheem/staff.sock",
		PublicOrigin:    "https://tickets.example.org",
		ShutdownTimeout: 7 * time.Second,
		Payment:         Payment{Provider: "fake"},
	}
	want.Delivery = Delivery{TicketKey: key(), GateOrigin: "https://gate.example.org", EmailProvider: "fake", SMSProvider: "fake"}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadAPIDefaultsShutdownTimeout(t *testing.T) {
	m := fullAPIEnv()
	delete(m, "SHUTDOWN_TIMEOUT")
	cfg, err := LoadAPI(env(m))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if cfg.ShutdownTimeout != DefaultShutdownTimeout {
		t.Fatalf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, DefaultShutdownTimeout)
	}
}

func TestLoadAPIReportsAllMissingVariables(t *testing.T) {
	_, err := LoadAPI(env(nil))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "API_PUBLIC_LISTEN", "API_STAFF_LISTEN", "PUBLIC_ORIGIN", "PAYMENT_PROVIDER"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

func TestLoadAPIRejectsBadShutdownTimeout(t *testing.T) {
	for _, v := range []string{"soon", "0s", "-1s"} {
		m := fullAPIEnv()
		m["SHUTDOWN_TIMEOUT"] = v
		_, err := LoadAPI(env(m))
		if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_TIMEOUT") {
			t.Errorf("SHUTDOWN_TIMEOUT=%q: got err %v", v, err)
		}
	}
}

func TestLoadWorker(t *testing.T) {
	cfg, err := LoadWorker(env(workerEnv(map[string]string{"DATABASE_URL": "postgres://db/tickets"})))
	if err != nil {
		t.Fatalf("LoadWorker: %v", err)
	}
	if cfg.DatabaseURL != "postgres://db/tickets" || cfg.ExpiryInterval != DefaultExpiryInterval {
		t.Fatalf("cfg = %+v", cfg)
	}
	cfg, err = LoadWorker(env(workerEnv(map[string]string{"EXPIRY_INTERVAL": "250ms"})))
	if err != nil || cfg.ExpiryInterval != 250*time.Millisecond {
		t.Fatalf("cfg = %+v err %v", cfg, err)
	}
	if _, err := LoadWorker(env(map[string]string{"DATABASE_URL": "x", "EXPIRY_INTERVAL": "often"})); err == nil {
		t.Fatal("bad EXPIRY_INTERVAL accepted")
	}
}

func TestLoadPublish(t *testing.T) {
	cfg, err := LoadPublish(env(map[string]string{"DATABASE_URL": "x", "PUBLISH_DIR": "/srv/buyer"}))
	if err != nil || cfg != (Publish{DatabaseURL: "x", Dir: "/srv/buyer"}) {
		t.Fatalf("cfg = %+v err %v", cfg, err)
	}
	if _, err := LoadPublish(env(nil)); err == nil || !strings.Contains(err.Error(), "PUBLISH_DIR") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadWorkerRequiresDatabaseURL(t *testing.T) {
	if _, err := LoadWorker(env(nil)); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("got err %v", err)
	}
}

func TestLoadDatabase(t *testing.T) {
	if url, err := LoadDatabase(env(map[string]string{"DATABASE_URL": "x"})); err != nil || url != "x" {
		t.Fatalf("%q %v", url, err)
	}
	if _, err := LoadDatabase(env(nil)); err == nil {
		t.Fatal("missing DATABASE_URL accepted")
	}
}

func TestPaymentProviders(t *testing.T) {
	bkash := map[string]string{"DATABASE_URL": "x", "PAYMENT_PROVIDER": "bkash",
		"BKASH_BASE_URL": "https://b", "BKASH_USERNAME": "u", "BKASH_PASSWORD": "p", "BKASH_APP_KEY": "k", "BKASH_APP_SECRET": "s"}
	cfg, err := LoadWorker(env(workerEnv(bkash)))
	want := Payment{Provider: "bkash", BkashBaseURL: "https://b", BkashUsername: "u", BkashPassword: "p", BkashAppKey: "k", BkashAppSecret: "s"}
	if err != nil || cfg.Payment != want {
		t.Fatalf("%+v %v", cfg.Payment, err)
	}
	_, err = LoadWorker(env(map[string]string{"DATABASE_URL": "x", "PAYMENT_PROVIDER": "bkash"}))
	for _, name := range []string{"BKASH_BASE_URL", "BKASH_USERNAME", "BKASH_PASSWORD", "BKASH_APP_KEY", "BKASH_APP_SECRET"} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("missing %s not reported: %v", name, err)
		}
	}
	if _, err := LoadWorker(env(map[string]string{"DATABASE_URL": "x", "PAYMENT_PROVIDER": "paypal"})); err == nil ||
		!strings.Contains(err.Error(), "fake, bkash or paystation") {
		t.Fatalf("unknown provider: %v", err)
	}
	ps, err := LoadWorker(env(workerEnv(map[string]string{"PAYMENT_PROVIDER": "paystation",
		"PAYSTATION_BASE_URL": "https://p", "PAYSTATION_MERCHANT_ID": "m", "PAYSTATION_PASSWORD": "pw"})))
	if err != nil || ps.Payment != (Payment{Provider: "paystation", PayStationBaseURL: "https://p", PayStationMerchantID: "m", PayStationPassword: "pw"}) {
		t.Fatalf("paystation %+v %v", ps.Payment, err)
	}
	_, err = LoadWorker(env(map[string]string{"DATABASE_URL": "x", "PAYMENT_PROVIDER": "paystation"}))
	for _, name := range []string{"PAYSTATION_BASE_URL", "PAYSTATION_MERCHANT_ID", "PAYSTATION_PASSWORD"} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("missing %s not reported: %v", name, err)
		}
	}
}

func TestDeliveryConfig(t *testing.T) {
	cfg, err := LoadWorker(env(workerEnv(map[string]string{"EMAIL_PROVIDER": "smtp", "SMTP_ADDR": "smtp.example.org:587",
		"EMAIL_FROM": "Kacheem Tickets <tickets@example.org>", "SMTP_USERNAME": "u", "SMTP_PASSWORD": "p"})))
	d := cfg.Delivery
	if err != nil || d.SMTPAddr != "smtp.example.org:587" || d.EmailFrom == "" || d.SMTPUsername != "u" || d.SMTPPassword != "p" ||
		cfg.PublicOrigin != "https://tickets.example.org" || len(d.TicketKey) != 32 {
		t.Fatalf("%+v %v", d, err)
	}
	cases := map[string]map[string]string{
		"SMTP_ADDR":      {"EMAIL_PROVIDER": "smtp"},
		"EMAIL_PROVIDER": {"EMAIL_PROVIDER": "pigeon"},
		"SMS_PROVIDER":   {"SMS_PROVIDER": "carrier"},
		"TICKET_KEY":     {"TICKET_KEY": "short"},
	}
	for name, over := range cases {
		if _, err := LoadWorker(env(workerEnv(over))); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err = LoadWorker(env(map[string]string{}))
	for _, name := range []string{"TICKET_KEY", "GATE_ORIGIN", "EMAIL_PROVIDER", "SMS_PROVIDER", "PUBLIC_ORIGIN"} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("missing %s not reported", name)
		}
	}
}
