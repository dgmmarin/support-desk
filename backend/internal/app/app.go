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
	"tourdesk/internal/store"
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
	// M10 read plane (read-only, tenant-scoped): /analytics/{operational,automation,quality,roi}.
	mux.Handle("/analytics/", analytics.Handler{DB: appDB})
	// M4 knowledge browser (tenant-scoped): /knowledge/{search,stale,retire}.
	mux.Handle("/knowledge/", knowledgebrowser.Handler{DB: appDB})
	// M8 canonical-answer promotion (tenant-scoped, human-gated): /promotion/{propose,approve}.
	mux.Handle("/promotion/", canonpromote.Handler{DB: appDB, Index: knowledgeindex.New(knowledgeindex.HashEmbedder{})})

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
