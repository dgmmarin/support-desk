// Package tonebank is the M8 tone-example bank (FR-M8-04): a per-tenant set of
// exemplary approved replies used as few-shot voice examples for generation, refreshed
// as the voice evolves and bounded by size and recency limits. An empty bank returns no
// examples, which generation reads as voice-profile-only (the generate default) — so an
// operator with no exemplars is never worse off.
//
// The bank is tenant-scoped through store.WithTenant (ADR-0015); the SQL lives in the
// store package (ListToneExamples). This package owns the bounding policy (size +
// recency) and the read accessor generation calls.
package tonebank

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
)

// Options bound the bank: at most MaxExamples exemplars (size), none older than MaxAge
// (recency). Zero fields fall back to bounded defaults.
type Options struct {
	MaxExamples int
	MaxAge      time.Duration
}

// defaults keep the few-shot prompt small and the voice current.
//
// ponytail: fixed 5 examples / 180 days (ceiling: not tuned per tenant). Upgrade path
// is Options carried from tenant config; the empty⇒voice-only contract is unchanged.
const (
	defaultMaxExamples = 5
	defaultMaxAge      = 180 * 24 * time.Hour
)

func (o Options) resolve() Options {
	if o.MaxExamples <= 0 {
		o.MaxExamples = defaultMaxExamples
	}
	if o.MaxAge <= 0 {
		o.MaxAge = defaultMaxAge
	}
	return o
}

// cutoff is the recency boundary: examples created before it are excluded.
func (o Options) cutoff(now time.Time) time.Time { return now.Add(-o.MaxAge) }

// Add appends an exemplary approved reply to the active tenant's bank (tx MUST come from
// store.WithTenant). The content must be PII-safe — promotion adds its PII-stripped body.
func Add(ctx context.Context, tx pgx.Tx, e store.ToneExample) (string, error) {
	return store.InsertToneExample(ctx, tx, e)
}

// Examples returns the tenant's newest exemplary replies within the size/recency limits,
// filtered by language, for use as few-shot generation examples (FR-M8-04). An empty
// bank returns nil ⇒ generation uses the voice profile only.
func Examples(ctx context.Context, tx pgx.Tx, language string, now time.Time, opts Options) ([]string, error) {
	o := opts.resolve()
	return store.ListToneExamples(ctx, tx, language, o.cutoff(now), o.MaxExamples)
}
