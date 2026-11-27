// Package httpapi builds the HTTP handlers behind the public (buyer) and
// staff (VPN-only) listeners. The two handlers are separate so a staff route
// can never be reached through the public listener, even when called directly.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const (
	// MaxBodyBytes caps every request body until a route sets its own limit.
	MaxBodyBytes = 64 << 10
	// CheckTimeout bounds each readiness dependency check.
	CheckTimeout = 2 * time.Second
)

// Check is one readiness dependency, such as PostgreSQL or Redis.
type Check struct {
	Name string
	Fn   func(context.Context) error
}

// NewStaff returns the handler for the VPN-only staff listener.
func NewStaff(checks []Check) http.Handler {
	mux := baseMux(checks)
	return withDefaults(mux)
}

func baseMux(checks []Check) *http.ServeMux {
	mux := http.NewServeMux()
	get(mux, "/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	get(mux, "/api/v1/ready", ready(checks))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "No such API route.")
	})
	return mux
}

// get registers a GET (and HEAD) route that answers other methods with a JSON 405.
func get(mux *http.ServeMux, path string, h http.HandlerFunc) {
	route(mux, "GET", "GET, HEAD", path, h)
}

// post registers a POST route that answers other methods with a JSON 405.
func post(mux *http.ServeMux, path string, h http.HandlerFunc) { route(mux, "POST", "POST", path, h) }

func route(mux *http.ServeMux, method, allow, path string, h http.HandlerFunc) {
	mux.HandleFunc(method+" "+path, h)
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
	})
}

func ready(checks []Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		failing := []string{}
		for _, c := range checks {
			ctx, cancel := context.WithTimeout(r.Context(), CheckTimeout)
			if c.Fn(ctx) != nil {
				failing = append(failing, c.Name)
			}
			cancel()
		}
		if len(failing) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "failing": failing})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// withDefaults applies headers and limits every API response needs.
// API responses are never cacheable by browsers, NGINX or a CDN.
func withDefaults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorWith(w, status, code, message, nil)
}

// writeErrorWith adds machine-readable details (field errors, remaining
// places, an existing order reference) beside the code and message.
func writeErrorWith(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	e := map[string]any{"code": code, "message": message}
	for k, v := range extra {
		e[k] = v
	}
	writeJSON(w, status, map[string]any{"error": e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
