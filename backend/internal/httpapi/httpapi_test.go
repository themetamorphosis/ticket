package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
)

func do(t *testing.T, h http.Handler, method, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON: %v", method, path, rec.Body.String(), err)
	}
	return rec, body
}

func ok(context.Context) error { return nil }

func newPublic(checks []Check) http.Handler {
	return NewPublic(PublicDeps{Sessions: scs.New(), Checks: checks, Logger: slog.New(slog.DiscardHandler)})
}

func handlers(checks ...Check) map[string]http.Handler {
	return map[string]http.Handler{"public": newPublic(checks),
		"staff": NewStaff(StaffDeps{Sessions: scs.New(), Checks: checks, Logger: slog.New(slog.DiscardHandler)})}
}

func TestHealth(t *testing.T) {
	for name, h := range handlers() {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/health", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s %s: status %d", name, method, rec.Code)
			}
		}
		_, body := do(t, h, http.MethodGet, "/api/v1/health")
		if body["status"] != "ok" {
			t.Errorf("%s: body %v", name, body)
		}
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	for name, h := range handlers() {
		for _, path := range []string{"/api/v1/health", "/api/v1/nope"} {
			rec, _ := do(t, h, http.MethodGet, path)
			for k, want := range map[string]string{
				"Cache-Control":          "no-store",
				"X-Content-Type-Options": "nosniff",
				"Content-Type":           "application/json; charset=utf-8",
			} {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("%s %s: %s = %q, want %q", name, path, k, got, want)
				}
			}
		}
	}
}

func TestReadyWhenAllChecksPass(t *testing.T) {
	for name, h := range handlers(Check{"postgres", ok}, Check{"redis", ok}) {
		rec, body := do(t, h, http.MethodGet, "/api/v1/ready")
		if rec.Code != http.StatusOK || body["status"] != "ready" {
			t.Errorf("%s: %d %v", name, rec.Code, body)
		}
	}
}

func TestReadyNamesFailingChecksWithoutLeakingErrors(t *testing.T) {
	failing := func(context.Context) error { return errors.New("password authentication failed for user app") }
	for name, h := range handlers(Check{"postgres", ok}, Check{"redis", failing}) {
		rec, body := do(t, h, http.MethodGet, "/api/v1/ready")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "password") {
			t.Errorf("%s: response leaks error text: %s", name, rec.Body.String())
		}
		got, _ := body["failing"].([]any)
		if body["status"] != "unavailable" || len(got) != 1 || got[0] != "redis" {
			t.Errorf("%s: body %v", name, body)
		}
	}
}

func TestReadyBoundsEachCheck(t *testing.T) {
	var deadline time.Time
	check := func(ctx context.Context) error {
		deadline, _ = ctx.Deadline()
		return nil
	}
	start := time.Now()
	do(t, newPublic([]Check{{"slow", check}}), http.MethodGet, "/api/v1/ready")
	if deadline.IsZero() || deadline.Sub(start) > CheckTimeout+time.Second {
		t.Fatalf("check deadline %v not bounded by %v", deadline, CheckTimeout)
	}
}

func TestUnknownAPIPathIsJSONNotFound(t *testing.T) {
	for name, h := range handlers() {
		for _, path := range []string{"/api/v1/nope", "/", "/api/v2/health"} {
			rec, body := do(t, h, http.MethodGet, path)
			e, _ := body["error"].(map[string]any)
			if rec.Code != http.StatusNotFound || e["code"] != "not_found" {
				t.Errorf("%s %s: %d %v", name, path, rec.Code, body)
			}
		}
	}
}

func TestPublicListenerRejectsStaffRoutes(t *testing.T) {
	for _, path := range []string{"/api/v1/ops/orders", "/api/v1/admissions"} {
		rec, _ := do(t, newPublic(nil), http.MethodPost, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("public %s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestWrongMethodIsJSON405(t *testing.T) {
	for name, h := range handlers() {
		for _, path := range []string{"/api/v1/health", "/api/v1/ready"} {
			rec, body := do(t, h, http.MethodPost, path)
			e, _ := body["error"].(map[string]any)
			if rec.Code != http.StatusMethodNotAllowed || e["code"] != "method_not_allowed" {
				t.Errorf("%s %s: %d %v", name, path, rec.Code, body)
			}
			if rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: Allow = %q", name, path, rec.Header().Get("Allow"))
			}
		}
	}
}

func TestRequestBodiesAreLimited(t *testing.T) {
	var readErr error
	h := withDefaults(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	big := strings.NewReader(strings.Repeat("x", MaxBodyBytes+1))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", big))
	var tooLarge *http.MaxBytesError
	if !errors.As(readErr, &tooLarge) {
		t.Fatalf("read error = %v, want MaxBytesError", readErr)
	}
}
