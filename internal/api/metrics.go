package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// GET /metrics exposes rekam's own request-level metrics in Prometheus text
// format, and — with no extra wiring — litestream's own per-tenant replication
// metrics too, whenever --replicate is on. litestream (internal/db/replicate.go)
// registers litestream_sync_count / litestream_sync_error_count / litestream_txid
// / litestream_disk_full etc., labeled by tenant db path, against the same
// process-wide default Prometheus registry promhttp.Handler serves here — see
// github.com/benbjohnson/litestream's db.go. That's the sharpest signal for
// "is replication actually still working": a climbing sync_error_count or a
// txid that's stopped advancing means backups have silently gone stale, which
// is exactly the failure mode that's invisible without this endpoint.
//
// These vars are package-level, registered exactly once via Go's normal
// var-init — NOT created inside NewServer, which runs many times across the
// test suite; re-registering a same-named collector against the default
// registry panics. gauges below are .Set() fresh at scrape time in
// handleMetrics rather than kept current by a background updater.
var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rekam_http_requests_total",
		Help: "HTTP requests served, by route pattern, method, and status code.",
	}, []string{"route", "method", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "rekam_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, by route pattern and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	rateLimitRejections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rekam_ratelimit_rejections_total",
		Help: "Requests rejected by a per-IP rate limiter, by limiter name.",
	}, []string{"limiter"})

	managerOpenHandles = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "rekam_manager_open_handles",
		Help: "Tenant SQLite handles currently open in the connection Manager.",
	})

	managerOpens = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "rekam_manager_opens",
		Help: "Cumulative tenant handle opens since process start (a Gauge, not a Counter — it's a periodic snapshot of an internal atomic count, not incremented here).",
	})

	managerCloses = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "rekam_manager_closes",
		Help: "Cumulative tenant handle closes since process start (see rekam_manager_opens for why this is a Gauge).",
	})
)

// handleMetrics serves GET /metrics. Admin-gated, like the rest of the
// operational surface (/admin/stats, /admin/grants) — route/latency
// breakdowns and replication sync-error counts aren't secret the way memory
// content is, but they're not something to hand out to an arbitrary internet
// caller either. A Prometheus scrape_config points its `authorization`
// credentials at the same REKAM_ADMIN_KEY already used for CLI access — no
// new secret to provision.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin key required"})
		return
	}
	mgr := s.eng.Manager()
	managerOpenHandles.Set(float64(mgr.OpenCount()))
	managerOpens.Set(float64(mgr.Opens()))
	managerCloses.Set(float64(mgr.Closes()))
	promhttp.Handler().ServeHTTP(w, r)
}

// statusRecorder captures the status code a handler wrote, defaulting to 200
// the way http.ResponseWriter itself does when a handler calls Write without
// ever calling WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// instrumentMetrics records a count and a latency observation for every
// request, labeled by the mux's registered route *pattern* (e.g.
// "/memory/{id}") rather than the literal request path — using the raw path
// would give a record's id its own metric series, and cardinality would grow
// without bound as the corpus does. mux.Handler(r) resolves the pattern a
// request will be served by without altering the request, so it's safe to
// call here, ahead of the full session/team/auth chain in `next` — the two
// are decoupled deliberately: mux is only consulted for its routing table,
// `next` is what actually serves the request and is what latency measures.
func (s *Server) instrumentMetrics(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		route := routeLabel(pattern)

		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sr, r)
		elapsed := time.Since(start).Seconds()

		httpRequestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(sr.status)).Inc()
		httpRequestDuration.WithLabelValues(route, r.Method).Observe(elapsed)
	})
}

// routeLabel strips the leading "METHOD " every pattern in server.go is
// registered with (e.g. "GET /memory/{id}" -> "/memory/{id}"), so the route
// label carries the path shape only — method is already its own label.
// Requests matching no route at all get "unmatched" (an empty pattern),
// still a single bounded label value, not one per bogus path someone probes.
func routeLabel(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		return pattern[i+1:]
	}
	return pattern
}
