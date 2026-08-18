// Package app wires the backend together: configuration, Postgres, NATS, the
// health endpoint, and correlation-id-aware HTTP. No business logic lives here —
// this is the skeleton every pipeline stage and service builds on (ISSUE-0001).
package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"tourdesk/internal/analytics"
	"tourdesk/internal/bus"
	"tourdesk/internal/canonpromote"
	"tourdesk/internal/clog"
	"tourdesk/internal/config"
	"tourdesk/internal/health"
	"tourdesk/internal/knowledgebrowser"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/queue"
	"tourdesk/internal/rbac"
	"tourdesk/internal/sso"
	"tourdesk/internal/store"

	"github.com/jackc/pgx/v5"
)

// Server is a running backend instance.
type Server struct {
	log   *slog.Logger
	db    *store.DB
	appDB *store.DB // non-superuser, RLS-bound pool for tenant-scoped read APIs (M10)
	bus   *bus.Bus
	http  *http.Server
	ln    net.Listener
}

// Start connects to Postgres and NATS, asserts the required PG extensions are
// present (fail fast), then begins serving the health endpoint on cfg.HTTPAddr.
// It returns once the listener is bound; the HTTP server runs in a goroutine.
func Start(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Server, error) {
	db, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.CheckExtensions(ctx); err != nil {
		db.Close()
		return nil, err // extensions asserted present at boot
	}

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		db.Close()
		return nil, err
	}

	// Separate non-superuser pool for tenant-scoped read APIs (M10): a superuser
	// connection bypasses RLS, so the analytics plane must not use the migration pool.
	appDB, err := store.Connect(ctx, cfg.AppDatabaseURL)
	if err != nil {
		b.Close()
		db.Close()
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/healthz", health.Handler{
		Timeout: 3 * time.Second,
		Checkers: []health.Checker{
			health.CheckerFunc{N: "postgres", F: db.Ping},
			health.CheckerFunc{N: "nats", F: b.Health},
		},
	})
	// M10 read plane (read-only, tenant-scoped): /analytics/{operational,automation,quality,roi,knowledge,compliance}.
	mux.Handle("/analytics/", analytics.Handler{DB: appDB})
	// M4 knowledge browser (tenant-scoped): /knowledge/{search,stale,retire}.
	mux.Handle("/knowledge/", knowledgebrowser.Handler{DB: appDB})
	// M8 canonical-answer promotion (tenant-scoped, human-gated): /promotion/{propose,approve}.
	// M11 RBAC guard (FR-M11-04): promotion is a privileged action — only a content-owner
	// (or senior/supervisor) may approve. The guard authenticates via SSO, resolves the
	// tenant from the signed credential and the subject's roles from the tenant store, and
	// refuses an insufficient role (403). Unconfigured SSO ⇒ DenyAll ⇒ 401 (fail-closed).
	verifier := ssoVerifier(cfg)
	roleSrc := storeRoleSource{db: appDB}
	promotion := canonpromote.Handler{DB: appDB, Index: knowledgeindex.New(knowledgeindex.HashEmbedder{})}
	mux.Handle("/promotion/", rbac.Guard{Verifier: verifier, Roles: roleSrc, Require: rbac.PermKnowledgeApprove, Next: promotion})
	// M7 agent-console queue (tenant-scoped): GET /queue (scored), POST /queue/{claim,resolve}.
	mux.Handle("/queue", queue.Handler{DB: appDB})
	mux.Handle("/queue/", queue.Handler{DB: appDB})

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		appDB.Close()
		b.Close()
		db.Close()
		return nil, err
	}

	hs := &http.Server{
		Handler:           correlationMiddleware(mux, logger),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	s := &Server{log: logger, db: db, appDB: appDB, bus: b, http: hs, ln: ln}
	go func() {
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped", "err", err)
		}
	}()
	return s, nil
}

// Addr is the bound listen address (useful when HTTPAddr requested port 0).
func (s *Server) Addr() string { return s.ln.Addr().String() }

// DB exposes the database handle (used by tests and later stages).
func (s *Server) DB() *store.DB { return s.db }

// Bus exposes the messaging handle.
func (s *Server) Bus() *bus.Bus { return s.bus }

// Close gracefully shuts the HTTP server and releases dependencies.
func (s *Server) Close(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	s.bus.Close()
	s.appDB.Close()
	s.db.Close()
	return err
}

// ssoVerifier builds the RBAC guard's SSO verifier from config. With an HS256 dev secret
// it verifies OIDC id-tokens; unconfigured it returns DenyAll so privileged planes fail
// closed (401) rather than open (SEC-05). Production wires an RS256 JWKS KeySource.
func ssoVerifier(cfg config.Config) sso.Verifier {
	if cfg.SSOHMACSecret == "" {
		return sso.DenyAll{}
	}
	return sso.OIDCVerifier{
		Issuer:   cfg.SSOIssuer,
		Audience: cfg.SSOAudience,
		Keys:     sso.StaticHMAC(cfg.SSOHMACSecret),
	}
}

// storeRoleSource adapts the tenant-scoped role store to rbac.RoleSource: it resolves a
// subject's roles under RLS (ADR-0015), so a subject in another tenant is invisible and
// an unprovisioned subject yields none (least privilege).
type storeRoleSource struct{ db *store.DB }

func (s storeRoleSource) Roles(ctx context.Context, tenant, subject string) ([]rbac.Role, error) {
	var roles []rbac.Role
	err := store.WithTenant(ctx, s.db.Pool, tenant, func(tx pgx.Tx) error {
		strs, e := store.RolesForSubject(ctx, tx, subject)
		if e != nil {
			return e
		}
		for _, r := range strs {
			roles = append(roles, rbac.Role(r))
		}
		return nil
	})
	return roles, err
}

// correlationMiddleware ensures every request carries a correlation id (honouring
// an inbound X-Correlation-ID, else minting one), echoes it back, threads it
// through the request context, and logs the request under it (NFR-R-01).
func correlationMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if id := r.Header.Get("X-Correlation-ID"); id != "" {
			ctx = clog.WithCorrelationID(ctx, id)
		} else {
			ctx = clog.Ensure(ctx)
		}
		w.Header().Set("X-Correlation-ID", clog.CorrelationID(ctx))
		clog.Logger(ctx, logger).Info("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
