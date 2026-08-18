package promote

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
)

// changeLogKind / refKey key the promotion audit record in the shared change_log
// (FR-M8-10): kind='policy', ref='<brand>/<intent>'. Reusing the change_log — rather
// than a parallel audit path — means the versioned, attributed, tenant-scoped trail
// (ADR-0015, INV-2) already exists for promotions too.
const changeLogKind = "policy"

func refKey(brand, intent string) string { return brand + "/" + intent }

// Request is a supervisor-proposed promotion for a (brand, intent) under the active
// tenant. TargetLevel must be exactly one above the current level. Supervisor is the
// attribution — required (FR-M6-10). Summary is an optional human note.
type Request struct {
	Brand       string
	Intent      string
	TargetLevel int
	Supervisor  string
	Summary     string
}

// Promote applies a supervisor-proposed trust-ladder promotion (FR-M6-10): it reads
// the current policy and the measured evidence (audit ratings + calibration),
// evaluates the criteria (CAL-01/02/03), and ONLY on an allowed decision raises the
// level and records a versioned, attributed change_log entry (kind='policy'). A
// blocked decision writes NOTHING (fail-closed) and returns its reasons; the caller
// surfaces them to the supervisor. All reads/writes are in one tenant-scoped
// transaction (ADR-0015). The bool reports whether the promotion was applied.
func Promote(ctx context.Context, db *store.DB, tenantID string, in Request, c Criteria) (Decision, bool, error) {
	if in.Brand == "" {
		in.Brand = "default"
	}
	if in.Intent == "" {
		return Decision{}, false, fmt.Errorf("promote: intent is required")
	}
	var dec Decision
	var applied bool
	err := store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		pol, e := store.GetAutonomyPolicy(ctx, tx, in.Brand, in.Intent)
		if e != nil {
			return e
		}
		correct, total, e := store.CountAuditRatings(ctx, tx, in.Intent)
		if e != nil {
			return e
		}
		m := Measured{AuditedCases: total, Correct: correct, Calibrated: pol.Calibrated, Evaluable: true}
		dec = Evaluate(in.Supervisor, pol.Level, in.TargetLevel, m, c)
		if !dec.Allowed {
			return nil // fail-closed: nothing written
		}
		pol.Level = in.TargetLevel
		if e := store.SetAutonomyPolicy(ctx, tx, in.Brand, in.Intent, pol); e != nil {
			return e
		}
		payload, e := json.Marshal(map[string]any{
			"from": in.TargetLevel - 1, "to": in.TargetLevel,
			"audited_cases": m.AuditedCases, "precision": m.Precision(),
		})
		if e != nil {
			return e
		}
		summary := in.Summary
		if summary == "" {
			summary = fmt.Sprintf("trust-ladder promotion %s/%s L%d→L%d", in.Brand, in.Intent, in.TargetLevel-1, in.TargetLevel)
		}
		if _, e := store.AppendChangeLogEntry(ctx, tx, store.ChangeLogEntry{
			Kind: changeLogKind, Ref: refKey(in.Brand, in.Intent),
			Actor: in.Supervisor, Summary: summary, Payload: payload,
		}); e != nil {
			return e
		}
		applied = true
		return nil
	})
	if err != nil {
		return Decision{}, false, err
	}
	return dec, applied, nil
}
