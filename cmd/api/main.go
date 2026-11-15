// Command api is the HTTP service under test.
//
//	api              serve HTTP
//	api migrate      apply db/migrations under an advisory lock, then exit
//	api healthcheck  GET /healthz on localhost (for the container healthcheck)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Adarsh16-30/BlastRadius/db/migrations"
	"github.com/Adarsh16-30/BlastRadius/internal/api"
	"github.com/Adarsh16-30/BlastRadius/internal/store"
)

const drainTimeout = 10 * time.Second

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "":
		err = serve(log)
	case "migrate":
		err = migrate(log)
	case "healthcheck":
		err = healthcheck()
	case "version": // shortcut: no-op so the one-shot migrate service has a declared healthcheck
		return
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func healthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8080/healthz", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: status %d", resp.StatusCode)
	}
	return nil
}

// migrate connects straight to MIGRATE_DB_HOST (host:port[,host:port]) as the migrator role;
// with several hosts the read-write one is used, so it still works after a failover.
func migrate(log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg := store.Config{
		Hosts: strings.Split(os.Getenv("MIGRATE_DB_HOST"), ","), Name: orDefault(os.Getenv("DB_NAME"), "blastradius"),
		User: os.Getenv("MIGRATE_DB_USER"), Password: os.Getenv("MIGRATE_DB_PASSWORD"),
	}
	badHost := func(h string) bool { return validHostPort(h) != nil }
	if slices.ContainsFunc(cfg.Hosts, badHost) || cfg.User == "" || cfg.Password == "" {
		return errors.New("MIGRATE_DB_HOST (host:port[,host:port]), MIGRATE_DB_USER and MIGRATE_DB_PASSWORD are required")
	}
	conn, err := pgx.Connect(ctx, cfg.DSN("read-write"))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	applied, err := store.Migrate(ctx, conn, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("migrations done", "applied", applied)
	return nil
}

func serve(log *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	var st *store.Store
	m, err := api.NewMetrics(func() (int32, int32) { return st.Acquired() })
	if err != nil {
		return err
	}
	st, err = store.New(ctx, cfg.DB, m.ObserveDB)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.New(api.Options{Store: st, Metrics: m, Log: log, ChaosEnabled: cfg.ChaosEnabled, Exit: os.Exit}),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "chaos", cfg.ChaosEnabled)

	select {
	case err := <-errc:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}
	log.Info("draining")
	dctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := srv.Shutdown(dctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil // deferred st.Close() closes the pools after the drain
}
