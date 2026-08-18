// Package verifystage runs pipeline stage 7 (Verify, M5) on the runner. It runs
// the independent verifier over the draft + sources and routes: a passing verdict
// proceeds to the Gate; a failing verdict — or a verifier outage — fails closed to
// human review (FR-M5-07, MOD-05). The verdict is carried forward as gate input.
package verifystage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/citation"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/verify"
)

// StageInput is what stage 7 consumes. Citations are the generator's per-claim
// machine-resolvable citations (FR-M5-02); SourceIDs are the retrieved chunk ids they
// must resolve against. They are consumed deterministically (never fed to the verifier
// model, keeping it independent — ADR-0007/MOD-03).
type StageInput struct {
	Draft     string              `json:"draft"`
	Sources   []string            `json:"sources,omitempty"`
	Citations []citation.Citation `json:"citations,omitempty"`
	SourceIDs []string            `json:"source_ids,omitempty"`
}

// VerifiedEvent carries the verdict downstream to the gate. Citations are carried on
// so the console/audit chain (INV-5) retains the resolved per-claim evidence.
type VerifiedEvent struct {
	CorrelationID    string              `json:"correlation_id"`
	Pass             bool                `json:"pass"`
	Flags            verify.Flags        `json:"flags"`
	CitationsResolve bool                `json:"citations_resolve"`
	Citations        []citation.Citation `json:"citations,omitempty"`
}

// gateReady reports whether a verified draft may proceed to the gate: the independent
// model verdict passes AND every machine-resolvable citation resolves to a retrieved
// source id (FR-M5-02). The citation check is deterministic and independent of the
// model (ADR-0007). Fail-closed: an unresolved citation blocks auto-send even on a
// passing model verdict.
func gateReady(v verify.Verdict, cites []citation.Citation, sourceIDs []string) bool {
	return v.Pass() && citation.AllResolve(cites, citation.SourceSet(sourceIDs...))
}

// Serve runs the Verify stage. gateSubject receives passing drafts; humanSubject
// receives failing verdicts and verifier errors.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, vr verify.Verifier, inStream, inSubject, gateSubject, humanSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "verify",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject,
		QuarantineSubject: humanSubject,
	}, func(hctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in StageInput
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		v, err := vr.Verify(hctx, in.Draft, in.Sources)
		if err != nil {
			return pipeline.Decision{}, err // verifier outage → failed verdict → human (MOD-05)
		}
		resolve := citation.AllResolve(in.Citations, citation.SourceSet(in.SourceIDs...))
		ready := gateReady(v, in.Citations, in.SourceIDs)
		evt := VerifiedEvent{
			CorrelationID:    env.CorrelationID,
			Pass:             ready,
			Flags:            v.Flags,
			CitationsResolve: resolve,
			Citations:        in.Citations,
		}
		subject := gateSubject
		if !ready {
			subject = humanSubject // failed verdict or unresolved citation → human
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
