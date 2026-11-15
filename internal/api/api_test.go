package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Adarsh16-30/BlastRadius/internal/api"
	"github.com/Adarsh16-30/BlastRadius/internal/store"
)

const goodID = "11111111-2222-3333-4444-555555555555"

type fakeStore struct {
	err     error
	created bool
	limit   int
}

func (f *fakeStore) Ping(context.Context) error { return f.err }
func (f *fakeStore) CreateOrder(_ context.Context, k, c string, a int64) (store.Order, bool, error) {
	return store.Order{ID: goodID, IdempotencyKey: k, CustomerID: c, AmountCents: a}, f.created, f.err
}
func (f *fakeStore) GetOrder(context.Context, string) (store.Order, error) {
	return store.Order{ID: goodID}, f.err
}
func (f *fakeStore) ListOrders(_ context.Context, n int) ([]store.Order, error) {
	f.limit = n
	return []store.Order{}, f.err
}
func (f *fakeStore) InsertSeq(context.Context, string, int64) (bool, error) { return f.created, f.err }
func (f *fakeStore) ListSeq(context.Context, string) ([]int64, error)       { return []int64{1, 2}, f.err }

func newServer(t *testing.T, f *fakeStore, chaos bool, exit func(int)) http.Handler {
	t.Helper()
	m, err := api.NewMetrics(func() (int32, int32) { return 1, 2 })
	if err != nil {
		t.Fatal(err)
	}
	return api.New(api.Options{Store: f, Metrics: m, Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), ChaosEnabled: chaos, Exit: exit})
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRoutes(t *testing.T) {
	idem := map[string]string{"Idempotency-Key": "k1"}
	tests := []struct {
		name   string
		store  fakeStore
		method string
		path   string
		body   string
		hdr    map[string]string
		want   int
	}{
		{"healthz", fakeStore{}, "GET", "/healthz", "", nil, 200},
		{"readyz ok", fakeStore{}, "GET", "/readyz", "", nil, 200},
		{"readyz db down", fakeStore{err: errors.New("down")}, "GET", "/readyz", "", nil, 503},
		{"order created", fakeStore{created: true}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, idem, 201},
		{"order repeat key", fakeStore{}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, idem, 200},
		{"order no key", fakeStore{}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, nil, 400},
		{"order zero amount", fakeStore{}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":0}`, idem, 400},
		{"order unknown field", fakeStore{}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5,"x":1}`, idem, 400},
		{"order bad json", fakeStore{}, "POST", "/v1/orders", `{`, idem, 400},
		{"order long key", fakeStore{}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, map[string]string{"Idempotency-Key": strings.Repeat("k", 129)}, 400},
		{"order db error", fakeStore{err: errors.New("boom")}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, idem, 500},
		{"order deadline", fakeStore{err: context.DeadlineExceeded}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, idem, 504},
		{"order NUL in customer", fakeStore{}, "POST", "/v1/orders", `{"customer_id":"a\u0000b","amount_cents":5}`, idem, 400},
		{"order oversize body", fakeStore{}, "POST", "/v1/orders", `{"customer_id":"` + strings.Repeat("a", 70<<10) + `","amount_cents":5}`, idem, 413},
		{"order statement timeout", fakeStore{err: &pgconn.PgError{Code: "57014"}}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, idem, 504},
		{"order client cancelled", fakeStore{err: context.Canceled}, "POST", "/v1/orders", `{"customer_id":"a","amount_cents":5}`, idem, 499},
		{"seq NUL in path", fakeStore{}, "GET", "/v1/seq/a%00b", "", nil, 400},
		{"get order", fakeStore{}, "GET", "/v1/orders/" + goodID, "", nil, 200},
		{"get order bad id", fakeStore{}, "GET", "/v1/orders/nope", "", nil, 400},
		{"get order missing", fakeStore{err: store.ErrNotFound}, "GET", "/v1/orders/" + goodID, "", nil, 404},
		{"list default", fakeStore{}, "GET", "/v1/orders", "", nil, 200},
		{"list bad limit", fakeStore{}, "GET", "/v1/orders?limit=abc", "", nil, 400},
		{"list zero limit", fakeStore{}, "GET", "/v1/orders?limit=0", "", nil, 400},
		{"seq created", fakeStore{created: true}, "POST", "/v1/seq", `{"writer_id":"w","seq":1}`, nil, 201},
		{"seq duplicate", fakeStore{}, "POST", "/v1/seq", `{"writer_id":"w","seq":1}`, nil, 200},
		{"seq invalid", fakeStore{}, "POST", "/v1/seq", `{"writer_id":"","seq":1}`, nil, 400},
		{"seq zero", fakeStore{}, "POST", "/v1/seq", `{"writer_id":"w","seq":0}`, nil, 400},
		{"get seq", fakeStore{}, "GET", "/v1/seq/w", "", nil, 200},
		{"get seq db error", fakeStore{err: errors.New("x")}, "GET", "/v1/seq/w", "", nil, 500},
		{"metrics", fakeStore{}, "GET", "/metrics", "", nil, 200},
		{"debug disabled alloc", fakeStore{}, "POST", "/debug/alloc?mb=1", "", nil, 404},
		{"debug disabled crash", fakeStore{}, "POST", "/debug/crash", "", nil, 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.store
			rec := do(newServer(t, &f, false, nil), tc.method, tc.path, tc.body, tc.hdr)
			if rec.Code != tc.want {
				t.Fatalf("%s %s = %d, want %d, body %s", tc.method, tc.path, rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestListLimitIsCapped(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{{"", 20}, {"?limit=5", 5}, {"?limit=100", 100}, {"?limit=5000", 100}}
	for _, tc := range tests {
		f := &fakeStore{}
		do(newServer(t, f, false, nil), "GET", "/v1/orders"+tc.query, "", nil)
		if f.limit != tc.want {
			t.Errorf("query %q: store limit %d, want %d", tc.query, f.limit, tc.want)
		}
	}
}

func TestRequestID(t *testing.T) {
	h := newServer(t, &fakeStore{}, false, nil)
	if id := do(h, "GET", "/healthz", "", nil).Header().Get("X-Request-Id"); len(id) != 16 {
		t.Errorf("generated id %q, want 16 hex chars", id)
	}
	if id := do(h, "GET", "/healthz", "", map[string]string{"X-Request-Id": "abc-123"}).Header().Get("X-Request-Id"); id != "abc-123" {
		t.Errorf("echoed id %q", id)
	}
	if id := do(h, "GET", "/healthz", "", map[string]string{"X-Request-Id": "bad id\nx"}).Header().Get("X-Request-Id"); id == "bad id\nx" {
		t.Error("unsafe request id must be replaced")
	}
}

func TestMetricsOutput(t *testing.T) {
	h := newServer(t, &fakeStore{}, false, nil)
	do(h, "GET", "/healthz", "", nil)
	out := do(h, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`http_requests_total{code="200",method="GET",route="/healthz"} 1`,
		"http_request_duration_seconds_bucket",
		`db_pool_acquired_conns{pool="write"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q\n%s", want, out)
		}
	}
}

func TestDebugRoutesWhenChaosEnabled(t *testing.T) {
	code := -1
	h := newServer(t, &fakeStore{}, true, func(c int) { code = c })
	if rec := do(h, "POST", "/debug/alloc?mb=1", "", nil); rec.Code != 200 {
		t.Errorf("alloc = %d", rec.Code)
	}
	if rec := do(h, "POST", "/debug/alloc?mb=0", "", nil); rec.Code != 400 {
		t.Errorf("alloc mb=0 = %d", rec.Code)
	}
	if rec := do(h, "POST", "/debug/crash", "", nil); rec.Code != 202 || code != 1 {
		t.Errorf("crash = %d exit=%d", rec.Code, code)
	}
}

func TestObserveDBAppearsInMetrics(t *testing.T) {
	m, err := api.NewMetrics(nil)
	if err != nil {
		t.Fatal(err)
	}
	m.ObserveDB("write", 10*time.Millisecond)
	h := api.New(api.Options{Store: &fakeStore{}, Metrics: m, Log: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if out := do(h, "GET", "/metrics", "", nil).Body.String(); !strings.Contains(out, `db_query_duration_seconds_count{pool="write"} 1`) {
		t.Errorf("db histogram missing:\n%s", out)
	}
}
