// Package gatestage runs the deterministic gate (stage 8) as a pipeline stage.
// The decision stays pure (gate.Evaluate, SR-M6-01); this package adapts it to
// the runner (pipeline §3) and persists the auditable GateEvaluation before
// routing (FR-M7-15, INV-5). Persistence is idempotent (NFR-S-04) and fail-closed
// (ADR-0001): a case is never routed auto_send unless its evaluation was durably
// recorded.
package gatestage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/gate"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
)

// OutSubject maps a gate result to its output subject under base.
func OutSubject(base string, r gate.Result) string {
	switch r.Route {
	case gate.RouteSend:
		return base + ".send"
	case gate.RouteSpecialistQueue:
		return base + ".specialist"
	default:
		return base + ".queue"
	}
}

// Serve runs the gate stage. If db is non-nil, each evaluation is persisted under
// the case's tenant scope before routing; a persistence failure fails the case
// closed (routed to human, never auto_send). Passing a nil db disables
// persistence — for pure-routing tests only; production always injects a store.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, db *store.DB, inStream, inSubject, outBase string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "gate",
		Stream:            inStream,
		Subject:           inSubject,
		Durable:           "gate-stage",
		HumanSubject:      outBase + ".queue",
		QuarantineSubject: outBase + ".quarantine",
	}, func(ctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in gate.Input
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human queue
		}
		res := gate.Evaluate(in)

		if db != nil {
			if err := persist(ctx, db, env, res); err != nil {
				// No durable audit record ⇒ do not route (esp. not auto_send).
				return pipeline.Decision{}, fmt.Errorf("gate: persist evaluation: %w", err)
			}
		}
		return pipeline.Decision{Subject: OutSubject(outBase, res), Payload: res}, nil
	})
}

func persist(ctx context.Context, db *store.DB, env pipeline.Envelope, res gate.Result) error {
	conditions, err := json.Marshal(res.Conditions)
	if err != nil {
		return err
	}
	return store.WithTenant(ctx, db.Pool, env.TenantID, func(tx pgx.Tx) error {
		_, e := store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
			ConversationID: env.ConversationID,
			DraftID:        env.DraftID,
			Outcome:        string(res.Outcome),
			Route:          string(res.Route),
			Conditions:     conditions,
		})
		return e
	})
}
