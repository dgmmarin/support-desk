// Package clog carries a per-case correlation id through context.Context and
// binds it onto structured (slog) log records. Every stage of a case shares one
// id so its telemetry can be traced end to end (NFR-R-01).
package clog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

type ctxKey struct{}

// FieldName is the log attribute key under which the correlation id is emitted.
const FieldName = "correlation_id"

// WithCorrelationID returns a context carrying the given correlation id.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// CorrelationID returns the correlation id on ctx, or "" if none is set.
func CorrelationID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// NewID mints a fresh random correlation id (128 bits, hex).
func NewID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error on supported platforms; a short
	// read would still yield a usable (if lower-entropy) id, which is fine for a
	// trace tag.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Ensure returns a context guaranteed to carry a correlation id, generating one
// if absent. Use at trust boundaries (e.g. inbound HTTP) so every case is traceable.
func Ensure(ctx context.Context) context.Context {
	if CorrelationID(ctx) == "" {
		return WithCorrelationID(ctx, NewID())
	}
	return ctx
}

// Logger returns base enriched with the context's correlation id (when present),
// so every record it emits is attributable to the case.
func Logger(ctx context.Context, base *slog.Logger) *slog.Logger {
	if id := CorrelationID(ctx); id != "" {
		return base.With(slog.String(FieldName, id))
	}
	return base
}
