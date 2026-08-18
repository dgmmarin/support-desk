package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/casepipe"
	"tourdesk/internal/deliver"
	"tourdesk/internal/pipeline"
)

// Sandbox / test replay mode (FR-M11-07). A tenant runs real or synthetic cases
// through the decision spine to see the would-be outcome BEFORE going live — without
// sending anything. Sending is PHYSICALLY impossible, not policy-disabled (NFR-R-04):
// the spine (casepipe) has no Deliver stage, and the sandbox never constructs a
// Sender. This reuses the exact no-Sender replay guarantee proven since ISSUE-0030 —
// there is no separate "dry-run flag" that could be bypassed.

// AssertNoSend re-proves, at the sandbox seam, that a send is physically impossible:
// building a Deliver stage with the sandbox's (nil) sender yields ErrSendImpossible.
// Replay calls this before running so a misconfiguration fails closed loudly rather
// than sending during setup validation.
func AssertNoSend() error {
	var noSender deliver.Sender // nil — the sandbox wires no sender
	if _, err := deliver.New(noSender, nil); !errors.Is(err, deliver.ErrSendImpossible) {
		return fmt.Errorf("onboarding: sandbox is not send-safe (NFR-R-04): got %v", err)
	}
	return nil
}

// Result is one replayed case's would-be outcome. Route is the terminal decision the
// gate reached (auto_send | human_review | specialist_queue | filed); nothing was sent.
type Result struct {
	CorrelationID string
	Route         string
	Draft         string
}

// Replay drives cases through an already-wired decision spine (casepipe.Wire) and
// returns each case's would-be outcome, keyed by correlation id. It asserts send is
// impossible before running (NFR-R-04) and never constructs a Deliver stage, so
// nothing is sent. termStream must capture the spine's terminal subjects (spt.>).
// tenantID scopes every published case to the tenant under test (ADR-0015).
func Replay(ctx context.Context, js jetstream.JetStream, subj casepipe.Subjects, termStream string, tenantID string, cases map[string]casepipe.Case) (map[string]Result, error) {
	if err := AssertNoSend(); err != nil {
		return nil, err
	}
	if tenantID == "" {
		return nil, fmt.Errorf("onboarding: sandbox replay requires a tenant scope (ADR-0015)")
	}
	for corr, c := range cases {
		payload, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		env, err := json.Marshal(pipeline.Envelope{
			CorrelationID: corr, ConversationID: corr, TenantID: tenantID, Payload: payload,
		})
		if err != nil {
			return nil, err
		}
		if _, err := js.Publish(ctx, subj.ScreenIn, env); err != nil {
			return nil, fmt.Errorf("onboarding: sandbox publish %q: %w", corr, err)
		}
	}

	// Drain the terminal subjects until every case has reached a decision. The spine
	// stamps Case.Terminal at the deciding stage; there is no send anywhere in the flow.
	cons, err := js.CreateOrUpdateConsumer(ctx, termStream, jetstream.ConsumerConfig{
		FilterSubject: "spt.>", AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("onboarding: sandbox terminal consumer: %w", err)
	}
	results := make(map[string]Result, len(cases))
	deadline := time.Now().Add(20 * time.Second)
	for len(results) < len(cases) && time.Now().Before(deadline) {
		m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
		if err != nil {
			continue // no message in the window — retry until the deadline
		}
		_ = m.Ack()
		var env pipeline.Envelope
		if err := json.Unmarshal(m.Data(), &env); err != nil {
			return nil, err
		}
		if _, ok := cases[env.CorrelationID]; !ok {
			continue // not one of ours
		}
		var c casepipe.Case
		if err := json.Unmarshal(env.Payload, &c); err != nil {
			return nil, err
		}
		results[env.CorrelationID] = Result{CorrelationID: env.CorrelationID, Route: c.Terminal, Draft: c.Draft}
	}
	if len(results) < len(cases) {
		return results, fmt.Errorf("onboarding: sandbox replay timed out (%d/%d cases decided)", len(results), len(cases))
	}
	return results, nil
}
