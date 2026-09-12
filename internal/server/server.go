// Package server assembles the chi router, middleware chain, and http.Server.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/disillusioned-labs/expense/internal/config"
	"github.com/disillusioned-labs/expense/internal/handler"
	approvalhandler "github.com/disillusioned-labs/expense/internal/handler/approval"
	documenthandler "github.com/disillusioned-labs/expense/internal/handler/document"
	"github.com/disillusioned-labs/expense/internal/handler/health"
	projecthandler "github.com/disillusioned-labs/expense/internal/handler/project"
	rolehandler "github.com/disillusioned-labs/expense/internal/handler/role"
	transactionhandler "github.com/disillusioned-labs/expense/internal/handler/transaction"
	"github.com/disillusioned-labs/platform/authkit"
	"github.com/disillusioned-labs/platform/cache"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Server struct {
	http   *http.Server
	log    *slog.Logger
	health *health.Handler
}

// keyByRemoteIP buckets rate-limit counters by r.RemoteAddr, the unspoofable
// TCP peer (see the RealIP note in New). On the rare parse failure we fall
// back to the raw value so limiting still applies.
func keyByRemoteIP(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr, nil
	}
	return host, nil
}

// redisPinger adapts *goredis.Client to health.Pinger.
type redisPinger struct{ rdb *goredis.Client }

func (p redisPinger) Ping(ctx context.Context) error { return p.rdb.Ping(ctx).Err() }

// Deps carries everything the router needs. Adding a resource adds a field
// here; New's signature never changes.
type Deps struct {
	Pool          *pgxpool.Pool
	Redis         *goredis.Client
	RedisRequired bool
	Cache         cache.Cache
	Verifier      *authkit.Verifier
	AuthErrorHandler authkit.HTTPErrorHandler

	RoleHandler        *rolehandler.Handler
	ProjectHandler     *projecthandler.Handler
	TransactionHandler *transactionhandler.Handler
	DocumentHandler    *documenthandler.Handler
	ApprovalHandler    *approvalhandler.Handler
}

// New assembles the router - middleware chain and probes, plus the /api/v1
// subtree with its rate limiter.
func New(cfg *config.Config, log *slog.Logger, deps Deps) *Server {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	// No RealIP middleware on purpose: it trusts X-Forwarded-For / X-Real-IP
	// unconditionally, letting any client spoof its address (and evade the
	// rate limiter). Deployments behind a trusted proxy should add their own
	// middleware that only honors forwarded headers from that proxy.
	r.Use(requestIDResponse)
	r.Use(routePattern)
	r.Use(requestLogger(log))
	r.Use(recoverPanics(log))
	// Per-request deadline: downstream ctx-aware calls (pgx, redis) abort
	// when it fires and the client gets a 504.
	r.Use(chimw.Timeout(cfg.Server.RequestTimeout))

	// Unknown routes and wrong methods use the same error envelope as the
	// API. Registered before Mount so sub-routers inherit both handlers.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		handler.WriteError(w, http.StatusNotFound, handler.CodeNotFound, "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		handler.WriteError(w, http.StatusMethodNotAllowed, handler.CodeMethodNotAllowed, "method not allowed")
	})

	required := map[string]health.Pinger{"postgres": deps.Pool}
	optional := map[string]health.Pinger{}
	if deps.Redis != nil {
		if deps.RedisRequired {
			required["redis"] = redisPinger{rdb: deps.Redis}
		} else {
			optional["redis"] = redisPinger{rdb: deps.Redis}
		}
	}
	healthHandler := health.NewHandler(required, optional)
	r.Get("/healthz", healthHandler.Liveness)
	r.Get("/readyz", healthHandler.Readiness)
	// No /metrics on purpose: metrics are pushed over OTLP (see
	// platform/telemetry). A scrape endpoint would put route patterns, latency
	// distributions and pool sizes on an unauthenticated public port.

	r.Route("/api/v1", func(r chi.Router) {
		// Rate limit only the API subtree so health probes - hit constantly by
		// orchestrators - are never throttled. Requests is the per-IP count
		// allowed in each window; over it returns 429 through the standard
		// envelope.
		if cfg.RateLimit.Enabled {
			// Not httprate's KeyByRealIP: it trusts forwarded headers
			// (GHSA-9g5q-2w5x-hmxf).
			r.Use(httprate.LimitBy(
				cfg.RateLimit.Requests,
				cfg.RateLimit.Window,
				keyByRemoteIP,
				httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
					handler.WriteError(w, http.StatusTooManyRequests, handler.CodeRateLimited, "rate limit exceeded")
				}),
			))
		}

		// Zero trust: setiap request API diverifikasi sendiri terhadap JWKS
		// identity. Tidak ada endpoint API publik - probe hidup di luar /api/v1.
		r.Use(func(next http.Handler) http.Handler {
			return deps.Verifier.Middleware(next, deps.AuthErrorHandler)
		})

		deps.RoleHandler.ProtectedRoutes(r)
		deps.ProjectHandler.ProtectedRoutes(r)
		deps.TransactionHandler.ProtectedRoutes(r)
		deps.DocumentHandler.ProtectedRoutes(r)
		deps.ApprovalHandler.ProtectedRoutes(r)
	})

	// otelhttp wraps the whole router: creates the server span, extracts
	// incoming trace context, and records HTTP metrics. The filter drops probes
	// from both signals - always-fast probe traffic in the same histogram drags
	// the p99 down and dilutes the error-ratio denominator.
	root := otelhttp.NewHandler(r, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool { return !isProbe(r.URL.Path) }),
	)

	return &Server{
		http: &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
			Handler:      root,
			ReadTimeout:  cfg.Server.ReadTimeout,
			WriteTimeout: cfg.Server.WriteTimeout,
			IdleTimeout:  cfg.Server.IdleTimeout,
		},
		log:    log,
		health: healthHandler,
	}
}

// BeginDrain flips /readyz to 503 while continuing to serve traffic, so an
// orchestrator can remove this instance from rotation before Shutdown starts.
func (s *Server) BeginDrain() { s.health.BeginDrain() }

// Start blocks until the listener fails or Shutdown is called.
func (s *Server) Start() error {
	s.log.Info("http server listening", "addr", s.http.Addr)
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// Shutdown drains in-flight requests within ctx and closes the listener.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
