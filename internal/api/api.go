// Package api holds the HTTP handlers, middleware and routing.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Adarsh16-30/BlastRadius/internal/store"
)

const (
	maxBody      = 64 << 10
	maxLimit     = 100
	defaultLimit = 20
	maxFieldLen  = 128
	maxAllocMB   = 512
	readyTimeout = 500 * time.Millisecond
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Store is what the handlers need from the database layer.
type Store interface {
	Ping(ctx context.Context) error
	CreateOrder(ctx context.Context, key, customer string, amount int64) (store.Order, bool, error)
	GetOrder(ctx context.Context, id string) (store.Order, error)
	ListOrders(ctx context.Context, limit int) ([]store.Order, error)
	InsertSeq(ctx context.Context, writer string, seq int64) (bool, error)
	ListSeq(ctx context.Context, writer string) ([]int64, error)
}

// Options configure the server.
type Options struct {
	Store        Store
	Metrics      *Metrics
	Log          *slog.Logger
	ChaosEnabled bool
	Exit         func(code int) // injected so tests can observe /debug/crash
}

type server struct {
	Options
	mu   sync.Mutex
	held [][]byte // memory held by /debug/alloc
}

// badText reports text Postgres would reject (NUL, invalid UTF-8), so it becomes a 400, not a 500.
func badText(s string) bool { return strings.ContainsRune(s, 0) || !utf8.ValidString(s) }

// pgStatementTimeout is SQLSTATE query_canceled, raised when statement_timeout fires.
const pgStatementTimeout = "57014"

// New returns the routed, wrapped handler.
func New(o Options) http.Handler {
	s := &server{Options: o}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("POST /v1/orders", s.createOrder)
	mux.HandleFunc("GET /v1/orders/{id}", s.getOrder)
	mux.HandleFunc("GET /v1/orders", s.listOrders)
	mux.HandleFunc("POST /v1/seq", s.postSeq)
	mux.HandleFunc("GET /v1/seq/{writer_id}", s.getSeq)
	mux.Handle("GET /metrics", o.Metrics.handler)
	if o.ChaosEnabled {
		mux.HandleFunc("POST /debug/alloc", s.debugAlloc)
		mux.HandleFunc("POST /debug/crash", s.debugCrash)
	}
	return wrap(mux, o.Log, o.Metrics)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // client gone: nothing useful to do
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// storeErr maps a database error to a response without leaking internals.
func storeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, context.DeadlineExceeded) || isStatementTimeout(err):
		logFrom(r.Context()).Warn("deadline exceeded", "err", err)
		writeErr(w, http.StatusGatewayTimeout, "deadline exceeded")
	case errors.Is(err, context.Canceled):
		logFrom(r.Context()).Info("client cancelled", "err", err)
		writeErr(w, 499, "client closed request") // nginx convention; client is gone anyway
	default:
		logFrom(r.Context()).Error("store error", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

func isStatementTimeout(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == pgStatementTimeout
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
			return false
		}
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		logFrom(r.Context()).Warn("not ready", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) createOrder(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > maxFieldLen || badText(key) {
		writeErr(w, http.StatusBadRequest, "Idempotency-Key header required (max 128 chars)")
		return
	}
	var in struct {
		CustomerID  string `json:"customer_id"`
		AmountCents int64  `json:"amount_cents"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.CustomerID == "" || len(in.CustomerID) > maxFieldLen || badText(in.CustomerID) || in.AmountCents <= 0 {
		writeErr(w, http.StatusBadRequest, "customer_id (max 128 chars) and amount_cents > 0 required")
		return
	}
	o, created, err := s.Store.CreateOrder(r.Context(), key, in.CustomerID, in.AmountCents)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, o)
}

func (s *server) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "id must be a uuid")
		return
	}
	o, err := s.Store.GetOrder(r.Context(), id)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *server) listOrders(w http.ResponseWriter, r *http.Request) {
	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxLimit)
	}
	orders, err := s.Store.ListOrders(r.Context(), limit)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func (s *server) postSeq(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WriterID string `json:"writer_id"`
		Seq      int64  `json:"seq"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.WriterID == "" || len(in.WriterID) > maxFieldLen || badText(in.WriterID) || in.Seq < 1 {
		writeErr(w, http.StatusBadRequest, "writer_id (max 128 chars) and seq >= 1 required")
		return
	}
	created, err := s.Store.InsertSeq(r.Context(), in.WriterID, in.Seq)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, map[string]any{"writer_id": in.WriterID, "seq": in.Seq})
}

func (s *server) getSeq(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("writer_id")
	if len(id) > maxFieldLen || badText(id) {
		writeErr(w, http.StatusBadRequest, "writer_id too long")
		return
	}
	seqs, err := s.Store.ListSeq(r.Context(), id)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"writer_id": id, "seqs": seqs})
}

func (s *server) debugAlloc(w http.ResponseWriter, r *http.Request) {
	mb, err := strconv.Atoi(r.URL.Query().Get("mb"))
	if err != nil || mb < 1 || mb > maxAllocMB {
		writeErr(w, http.StatusBadRequest, "mb must be 1..512")
		return
	}
	buf := make([]byte, mb<<20)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1 // touch each page so the memory is really resident
	}
	s.mu.Lock()
	s.held = append(s.held, buf)
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *server) debugCrash(w http.ResponseWriter, r *http.Request) {
	logFrom(r.Context()).Warn("debug crash requested")
	w.WriteHeader(http.StatusAccepted)
	// ResponseController reaches the real writer through statusWriter.Unwrap.
	if err := http.NewResponseController(w).Flush(); err != nil {
		logFrom(r.Context()).Warn("flush before crash failed", "err", err)
	}
	s.Exit(1)
}
