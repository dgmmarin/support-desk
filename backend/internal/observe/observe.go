// Package observe is pipeline stage 10 (Observe). Given a case's terminal outcome
// it emits immutable, tenant-scoped telemetry — one row per stage fact, every row
// carrying the case's single correlation id (NFR-R-01) so the whole case
// reconstructs from one id. The M10 read plane consumes these rows; Observe never
// decides anything and never changes the send decision (pipeline.md §2 row 10:
// "Fails to: —" — it never fails the case). Unlike Deliver, Observe is present and
// functional in a replay build (NFR-R-04): it needs no Sender, only a store.
package observe

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/clog"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
)

// DecodeSignals maps a terminated case's wire payload to the Signals Observe
// records. The spine owns the case shape, so it supplies the decoder — observe
// depends on nothing upstream (no import cycle).
type DecodeSignals func(payload json.RawMessage) (Signals, error)

// Terminal outcomes a case can reach. An unknown/empty outcome degrades to
// human_review — telemetry never records auto_send by default (fail-closed).
const (
	RouteAutoSend        = "auto_send"
	RouteHumanReview     = "human_review"
	RouteSpecialistQueue = "specialist_queue"
	RouteFiled           = "filed"
)

// Signals is the terminated case's observable state — the fields Observe turns
// into per-stage telemetry. It is a plain view (no dependency on the spine's Case
// type) so observe imports nothing upstream.
type Signals struct {
	Route             string // terminal outcome (RouteAutoSend | RouteHumanReview | …)
	Injection         bool
	HardStop          bool
	RiskClass         int
	VerificationLevel int
	ChunkCount        int
	Retrieved         bool
	GuardPass         bool
	DraftOnly         bool
	VerifyPass        bool
}

// Events builds the per-stage telemetry for a terminated case. Every event carries
// the one correlation id (NFR-R-01) and the case clock's ts. A missing terminal
// outcome degrades to human_review (never auto_send).
func Events(correlationID string, s Signals, at time.Time) []store.TelemetryEvent {
	route := s.Route
	switch route {
	case RouteAutoSend, RouteHumanReview, RouteSpecialistQueue, RouteFiled:
	default:
		route = RouteHumanReview // fail-closed: unknown outcome is never auto_send
	}
	ev := func(stage, metric, value string) store.TelemetryEvent {
		return store.TelemetryEvent{CorrelationID: correlationID, Stage: stage, Metric: metric, Value: value, TS: at}
	}
	return []store.TelemetryEvent{
		ev("screen", "injection", b(s.Injection)),
		ev("screen", "hard_stop", b(s.HardStop)),
		ev("understand", "risk_class", strconv.Itoa(s.RiskClass)),
		ev("identify", "verification_level", strconv.Itoa(s.VerificationLevel)),
		ev("retrieve", "chunk_count", strconv.Itoa(s.ChunkCount)),
		ev("generate", "commitment_guard_clear", b(s.GuardPass)),
		ev("verify", "grounded", b(s.VerifyPass)),
		ev("gate", "terminal", route),
	}
}

func b(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// Observer is the Observe stage: it persists telemetry for terminated cases. It
// holds only a store — no Sender exists at stage 10, so a replay build constructs
// it exactly like production (NFR-R-04, contrast deliver.New).
type Observer struct {
	db    *store.DB
	clock func() time.Time
}

// New builds an Observe stage. Only a store is required (there is nothing to send),
// so this never reports "send impossible" — Observe runs in replay (NFR-R-04).
func New(db *store.DB, clock func() time.Time) *Observer {
	if clock == nil {
		clock = time.Now
	}
	return &Observer{db: db, clock: clock}
}

// Serve consumes terminated cases (the spine's terminal subjects) and persists
// their telemetry under the case's tenant scope. It NEVER fails the case: a
// persistence error is logged and the message is acknowledged — Observe is a
// post-decision observer, so it must not re-route an already-terminated case
// (pipeline.md §2 row 10, "Fails to: —"). The terminal-sink handler returns an
// empty subject so the runner acks without routing anything downstream.
func (o *Observer) Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, stream, subject, quarantineSubject string, decode DecodeSignals) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "observe",
		Stream:            stream,
		Subject:           subject,
		HumanSubject:      quarantineSubject, // only reached on an undecodable payload
		QuarantineSubject: quarantineSubject,
	}, func(ctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		sig, err := decode(env.Payload)
		if err != nil {
			return pipeline.Decision{}, err // malformed → runner quarantines/fails closed
		}
		events := Events(env.CorrelationID, sig, o.clock())
		perr := store.WithTenant(ctx, o.db.Pool, env.TenantID, func(tx pgx.Tx) error {
			return store.InsertTelemetryEvents(ctx, tx, events)
		})
		if perr != nil {
			// Never fail the case: the outcome already happened; drop the telemetry
			// and ack. ponytail: telemetry is best-effort under a DB outage (ceiling:
			// lost observability rows); upgrade path is a durable outbox for stage 10.
			clog.Logger(ctx, logger).Error("observe: persist telemetry failed; case unaffected", "err", perr)
		}
		return pipeline.Decision{}, nil // terminal sink: empty subject → runner acks, no route
	})
}
