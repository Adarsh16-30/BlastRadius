package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Metrics holds the OpenTelemetry instruments exported on /metrics.
// shortcut: retries, breaker_state and readonly_mode arrive with M3.
type Metrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
	dbQuery  metric.Float64Histogram
	handler  http.Handler
}

// AcquiredFunc reports checked-out connections per pool.
type AcquiredFunc func() (write, read int32)

// NewMetrics builds the OTel SDK with a private Prometheus registry.
func NewMetrics(acquired AcquiredFunc) (*Metrics, error) {
	reg := prometheus.NewRegistry()
	exp, err := otelprom.New(otelprom.WithRegisterer(reg), otelprom.WithoutScopeInfo(), otelprom.WithoutTargetInfo())
	if err != nil {
		return nil, fmt.Errorf("prometheus exporter: %w", err)
	}
	m := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exp)).Meter("blastradius/api")
	out := &Metrics{handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{})}
	if out.requests, err = m.Int64Counter("http_requests_total"); err != nil {
		return nil, fmt.Errorf("requests counter: %w", err)
	}
	buckets := metric.WithExplicitBucketBoundaries(.005, .01, .025, .05, .1, .25, .5, 1, 2.5)
	if out.duration, err = m.Float64Histogram("http_request_duration_seconds", metric.WithUnit("s"), buckets); err != nil {
		return nil, fmt.Errorf("duration histogram: %w", err)
	}
	if out.dbQuery, err = m.Float64Histogram("db_query_duration_seconds", metric.WithUnit("s"), buckets); err != nil {
		return nil, fmt.Errorf("db histogram: %w", err)
	}
	if acquired != nil {
		if _, err = m.Int64ObservableGauge("db_pool_acquired_conns", metric.WithInt64Callback(
			func(_ context.Context, o metric.Int64Observer) error {
				w, r := acquired()
				o.Observe(int64(w), metric.WithAttributes(attribute.String("pool", "write")))
				o.Observe(int64(r), metric.WithAttributes(attribute.String("pool", "read")))
				return nil
			})); err != nil {
			return nil, fmt.Errorf("pool gauge: %w", err)
		}
	}
	return out, nil
}

// ObserveDB records one query duration; its signature matches store.Observer.
func (m *Metrics) ObserveDB(pool string, d time.Duration) {
	m.dbQuery.Record(context.Background(), d.Seconds(), metric.WithAttributes(attribute.String("pool", pool)))
}

func (m *Metrics) observeHTTP(ctx context.Context, route, method string, code int, d time.Duration) {
	m.requests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("route", route), attribute.String("method", method), attribute.Int("code", code)))
	m.duration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("route", route)))
}
