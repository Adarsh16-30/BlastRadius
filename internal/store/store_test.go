package store_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Adarsh16-30/BlastRadius/db/migrations"
	"github.com/Adarsh16-30/BlastRadius/internal/store"
)

const dropAll = "DROP TABLE IF EXISTS orders, seq_log, schema_migrations"

// Integration tests need a real Postgres: set TEST_DATABASE_URL (see docs/evidence/M1.md).
func testURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return u
}

func connect(t *testing.T, url string) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func setup(t *testing.T) *store.Store {
	t.Helper()
	url := testURL(t)
	ctx := context.Background()
	conn := connect(t, url)
	if _, err := conn.Exec(ctx, dropAll); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, conn, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cc := conn.Config()
	s, err := store.New(ctx, store.Config{
		Hosts: []string{cc.Host + ":" + strconv.Itoa(int(cc.Port))}, Name: cc.Database, User: cc.User, Password: cc.Password,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMigrateIsIdempotentAndConcurrencySafe(t *testing.T) {
	url := testURL(t)
	ctx := context.Background()
	if _, err := connect(t, url).Exec(ctx, dropAll); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	counts := make([]int, 3)
	for i := range counts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := pgx.Connect(ctx, url)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = c.Close(ctx) }()
			applied, err := store.Migrate(ctx, c, migrations.FS)
			if err != nil {
				t.Error(err)
			}
			counts[i] = len(applied)
		}()
	}
	wg.Wait()
	if sum := counts[0] + counts[1] + counts[2]; sum != 1 {
		t.Fatalf("migration applied %d times across 3 concurrent runs, want 1 (%v)", sum, counts)
	}
}

func TestMigrateBadSQLRollsBack(t *testing.T) {
	c := connect(t, testURL(t))
	ctx := context.Background()
	bad := fstest.MapFS{"9999_bad.sql": {Data: []byte("CREATE TABLE tmp_bad(x int); SELECT nope;")}}
	if _, err := store.Migrate(ctx, c, bad); err == nil {
		t.Fatal("want error for bad SQL")
	}
	var n int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE tablename='tmp_bad'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("tmp_bad exists=%d err=%v, want rollback", n, err)
	}
}

func TestOrders(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	o1, created, err := s.CreateOrder(ctx, "k1", "alice", 500)
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	o2, created, err := s.CreateOrder(ctx, "k1", "bob", 900)
	if err != nil || created {
		t.Fatalf("repeat create: created=%v err=%v", created, err)
	}
	if o2.ID != o1.ID || o2.CustomerID != "alice" || o2.AmountCents != 500 {
		t.Fatalf("repeat key must return original row, got %+v want %+v", o2, o1)
	}
	if _, _, err := s.CreateOrder(ctx, "bad", "x", 0); err == nil {
		t.Fatal("amount 0 must violate CHECK")
	}
	got, err := s.GetOrder(ctx, o1.ID)
	if err != nil || got.ID != o1.ID {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.GetOrder(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, _, err := s.CreateOrder(ctx, "k2", "carol", 1); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListOrders(ctx, 1)
	if err != nil || len(list) != 1 || list[0].IdempotencyKey != "k2" {
		t.Fatalf("list newest: %+v %v", list, err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if w, r := s.Acquired(); w < 0 || r < 0 {
		t.Fatalf("acquired %d %d", w, r)
	}
}

func TestSeq(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	for _, n := range []int64{2, 1, 2} {
		if _, err := s.InsertSeq(ctx, "w", n); err != nil {
			t.Fatal(err)
		}
	}
	created, err := s.InsertSeq(ctx, "w", 1)
	if err != nil || created {
		t.Fatalf("duplicate seq: created=%v err=%v", created, err)
	}
	got, err := s.ListSeq(ctx, "w")
	if err != nil || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("list seq: %v %v", got, err)
	}
	if empty, err := s.ListSeq(ctx, "none"); err != nil || len(empty) != 0 {
		t.Fatalf("unknown writer: %v %v", empty, err)
	}
}

func TestDSN(t *testing.T) {
	c := store.Config{Hosts: []string{"a:1", "b:2"}, Name: "db", User: "u", Password: "p@ss/w:rd"}
	want := "postgres://u:p%40ss%2Fw%3Ard@a:1,b:2/db?connect_timeout=2&target_session_attrs=read-write"
	if got := c.DSN("read-write"); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}
