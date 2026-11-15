// Package store owns the pgx pools and every SQL statement the API runs.
package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

const (
	connectTimeout   = 2 * time.Second
	statementTimeout = "1000" // ms, set per connection (PRD 6.1)
	healthCheckEvery = 5 * time.Second
	writePoolMax     = 6
	readPoolMax      = 4
)

// Order is one row of the orders table.
type Order struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	CustomerID     string    `json:"customer_id"`
	AmountCents    int64     `json:"amount_cents"`
	CreatedAt      time.Time `json:"created_at"`
}

// Observer receives the duration of every query, labelled by pool name.
type Observer func(pool string, d time.Duration)

// Store runs queries on a write pool and a read pool.
type Store struct {
	write, read *pgxpool.Pool
	observe     Observer
}

// Config describes how to reach the database.
type Config struct {
	Hosts    []string // host:port, tried in order
	Name     string
	User     string
	Password string
}

// DSN builds a multi-host URL with the given target_session_attrs.
func (c Config) DSN(attrs string) string {
	q := url.Values{}
	q.Set("target_session_attrs", attrs)
	q.Set("connect_timeout", "2")
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     strings.Join(c.Hosts, ","),
		Path:     "/" + c.Name,
		RawQuery: q.Encode(),
	}
	return u.String()
}

func newPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	pc.MaxConns = maxConns
	pc.HealthCheckPeriod = healthCheckEvery
	pc.ConnConfig.ConnectTimeout = connectTimeout
	pc.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeout
	// MinConns stays 0 so the pool starts even while the database is down.
	return pgxpool.NewWithConfig(ctx, pc)
}

// New opens both pools lazily; it does not require the database to be up.
func New(ctx context.Context, c Config, observe Observer) (*Store, error) {
	w, err := newPool(ctx, c.DSN("read-write"), writePoolMax)
	if err != nil {
		return nil, fmt.Errorf("write pool: %w", err)
	}
	r, err := newPool(ctx, c.DSN("any"), readPoolMax)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("read pool: %w", err)
	}
	if observe == nil {
		observe = func(string, time.Duration) {}
	}
	return &Store{write: w, read: r, observe: observe}, nil
}

// Close closes both pools.
func (s *Store) Close() { s.write.Close(); s.read.Close() }

// Acquired reports connections currently checked out of each pool.
func (s *Store) Acquired() (write, read int32) {
	return s.write.Stat().AcquiredConns(), s.read.Stat().AcquiredConns()
}

func (s *Store) timed(pool string) func() {
	start := time.Now()
	return func() { s.observe(pool, time.Since(start)) }
}

// Ping runs SELECT 1 on the read pool.
func (s *Store) Ping(ctx context.Context) error {
	defer s.timed("read")()
	var one int
	if err := s.read.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}

const orderCols = "id::text, idempotency_key, customer_id, amount_cents, created_at"

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.IdempotencyKey, &o.CustomerID, &o.AmountCents, &o.CreatedAt)
	return o, err
}

// CreateOrder inserts an order, or returns the existing row for a repeated key.
// created is false when the key was already present.
func (s *Store) CreateOrder(ctx context.Context, key, customer string, amount int64) (o Order, created bool, err error) {
	defer s.timed("write")()
	o, err = scanOrder(s.write.QueryRow(ctx,
		`INSERT INTO orders (idempotency_key, customer_id, amount_cents) VALUES ($1,$2,$3)
		 ON CONFLICT (idempotency_key) DO NOTHING RETURNING `+orderCols, key, customer, amount))
	if err == nil {
		return o, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, fmt.Errorf("insert order: %w", err)
	}
	o, err = scanOrder(s.write.QueryRow(ctx, "SELECT "+orderCols+" FROM orders WHERE idempotency_key=$1", key))
	if err != nil {
		return Order{}, false, fmt.Errorf("load existing order: %w", err)
	}
	return o, false, nil
}

// GetOrder reads one order by id (a valid uuid string).
func (s *Store) GetOrder(ctx context.Context, id string) (Order, error) {
	defer s.timed("read")()
	o, err := scanOrder(s.read.QueryRow(ctx, "SELECT "+orderCols+" FROM orders WHERE id=$1::uuid", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("get order: %w", err)
	}
	return o, nil
}

// ListOrders returns the newest limit orders.
func (s *Store) ListOrders(ctx context.Context, limit int) ([]Order, error) {
	defer s.timed("read")()
	rows, err := s.read.Query(ctx, "SELECT "+orderCols+" FROM orders ORDER BY created_at DESC, id LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// InsertSeq records (writer, seq); created is false when it already existed.
func (s *Store) InsertSeq(ctx context.Context, writer string, seq int64) (created bool, err error) {
	defer s.timed("write")()
	tag, err := s.write.Exec(ctx, "INSERT INTO seq_log (writer_id, seq) VALUES ($1,$2) ON CONFLICT DO NOTHING", writer, seq)
	if err != nil {
		return false, fmt.Errorf("insert seq: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListSeq returns every seq stored for writer, ascending.
func (s *Store) ListSeq(ctx context.Context, writer string) ([]int64, error) {
	defer s.timed("read")()
	rows, err := s.read.Query(ctx, "SELECT seq FROM seq_log WHERE writer_id=$1 ORDER BY seq", writer)
	if err != nil {
		return nil, fmt.Errorf("list seq: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan seq: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
