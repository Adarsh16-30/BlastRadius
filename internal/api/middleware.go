package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RequestTimeout is the total per-request deadline (PRD 6.1 rule 1).
const RequestTimeout = 2 * time.Second

type ctxKey int

const loggerKey ctxKey = 1

// logFrom returns the request-scoped logger (carries request_id).
func logFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

// Unwrap lets http.ResponseController reach the underlying writer (Flush, etc.).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(c int) {
	w.code = c
	w.ResponseWriter.WriteHeader(c)
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand does not fail on supported platforms
	return hex.EncodeToString(b)
}

// validRequestID accepts a caller-supplied id only if it is short and printable-safe.
func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

// wrap adds request id, deadline, access log and metrics around next.
func wrap(next http.Handler, log *slog.Logger, m *Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		l := log.With("request_id", id)
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), loggerKey, l), RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		// Pattern is "GET /v1/orders/{id}"; the method has its own label, so keep only the path.
		route := "unmatched"
		if _, p, ok := strings.Cut(r.Pattern, " "); ok {
			route = p
		} else if r.Pattern != "" {
			route = r.Pattern
		}
		d := time.Since(start)
		m.observeHTTP(context.WithoutCancel(ctx), route, r.Method, sw.code, d)
		l.Info("request", "method", r.Method, "path", r.URL.Path, "route", route, "status", sw.code, "ms", d.Milliseconds())
	})
}
