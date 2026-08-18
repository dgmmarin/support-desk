package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Usage metering (FR-M11-05, M11 §5). This is an M11 read surface hosted on the shared
// tenant-scoped read plane: it aggregates the immutable/state records the pipeline already
// writes into the per-tenant metered set that feeds billing + unit economics — conversations,
// messages, auto-sends, tokens, storage. Like every M10/M11 aggregate it is READ-ONLY and
// tenant-scoped, and it honours the two load-bearing guardrails:
//
//   - Fail-closed metrics (M10 §6): a dimension whose source is absent renders as an explicit
//     gap, never a false zero. Only TOKENS is gapped today — the model-provider seam emits no
//     per-call token/cost usage yet (ISSUE-0023 / ECO ledger), so a fabricated number would
//     misprice a tenant. It becomes real with no interface change once the producer lands
//     (mirrors ISSUE-0033's cost-per-contact-after gap).
//   - Isolation (ADR-0015, FR-M11-01): require_tenant() runs first so a scopeless query FAILS
//     rather than silently returning empty; RLS scopes every row to cur_tenant().
//
// ponytail: M11 §5 specifies an append-only meter(tenant_id, dimension, qty, ts) ledger with
// buffer/backfill. This slice READS existing immutable records instead (the endorsed approach
// for the read-plane backlog item). Ceiling: no independent buffer, so metering availability
// tracks the source tables' availability. Upgrade path: the dedicated ledger + backfill worker.

// UsageCounts is the raw shape the SQL produces, before computeUsage decides gap vs present.
// Latest/HasRows anchor freshness; StorageBytes is a current-state footprint, the rest are the
// window's counts.
type UsageCounts struct {
	Conversations int   // conversations active in the window (by last_activity_at)
	Messages      int   // messages in the window (by created_at)
	AutoSends     int   // gate evaluations with outcome='auto_send' in the window (ADR-0001 spine)
	StorageBytes  int64 // current-state stored-text footprint (knowledge content + attachment text)
	HasRows       bool  // any conversation/message/auto-send row in the window (freshness anchor)
	Latest        time.Time
}

// UsageReport is the FR-M11-05 metered set. Conversations/messages/auto-sends/storage are real
// figures (a 0 is a legitimate zero for an idle/new tenant, never a source-missing gap); tokens
// is a gap indicator until a model-seam producer emits it.
type UsageReport struct {
	Formula       string    `json:"formula"`
	Conversations Metric    `json:"conversations"`
	Messages      Metric    `json:"messages"`
	AutoSends     Metric    `json:"auto_sends"`
	Tokens        Metric    `json:"tokens"`
	StorageBytes  Metric    `json:"storage_bytes"`
	Freshness     Freshness `json:"freshness"`
}

const usageFormula = "conversations = distinct conversations active in window (last_activity_at); " +
	"messages = messages in window (created_at); auto_sends = gate_evaluations outcome='auto_send' in " +
	"window (the deterministic send-decision spine, ADR-0001); storage_bytes = current-state stored-text " +
	"footprint (octet_length of knowledge_items.content + attachments.extracted_text); tokens require a " +
	"per-call token/cost producer on the model-provider seam (not emitted yet) — gapped, never fabricated"

// tokensGapReason names the missing producer so the gap is actionable, not silent.
const tokensGapReason = "source telemetry missing: per-call token/cost usage from the model-provider " +
	"seam (LLM, ISSUE-0023 / ECO cost ledger) not emitted yet; gapped, never a fabricated meter (FR-M11-05)"

// computeUsage turns the raw counts into the metered report. Pure and deterministic (no I/O, no
// wall-clock beyond the passed `now`) so it is unit-tested directly.
func computeUsage(c UsageCounts, now time.Time) UsageReport {
	return UsageReport{
		Formula:       usageFormula,
		Conversations: present("conversations", float64(c.Conversations)),
		Messages:      present("messages", float64(c.Messages)),
		AutoSends:     present("auto_sends", float64(c.AutoSends)),
		Tokens:        gap("tokens", tokensGapReason),
		StorageBytes:  present("storage_bytes", float64(c.StorageBytes)),
		Freshness:     freshness(c.Latest, c.HasRows, now),
	}
}

// usageWindowSQL counts conversations/messages/auto-sends in the window and the freshness anchor
// (latest of the three). require_tenant() (already run in the preflight) plus RLS scope the rows.
// The three sources are unioned so one aggregate pass produces every windowed count.
const usageWindowSQL = `
WITH ev AS (
  SELECT 'conversation'::text AS kind, id AS ref, last_activity_at AS ts
  FROM conversations WHERE last_activity_at >= $1 AND last_activity_at < $2
  UNION ALL
  SELECT 'message', id, created_at
  FROM messages WHERE created_at >= $1 AND created_at < $2
  UNION ALL
  SELECT 'auto_send', id, created_at
  FROM gate_evaluations WHERE outcome = 'auto_send' AND created_at >= $1 AND created_at < $2
)
SELECT
  count(*) FILTER (WHERE kind = 'conversation')::int,
  count(*) FILTER (WHERE kind = 'message')::int,
  count(*) FILTER (WHERE kind = 'auto_send')::int,
  max(ts)
FROM ev`

// usageStorageSQL sums the current-state stored-text footprint: knowledge item bodies plus the
// masked extracted text held on attachments. Raw attachment blobs are not retained (scanned and
// discarded, M1), so this measures stored TEXT, not raw object storage — named in the formula.
const usageStorageSQL = `
SELECT
  coalesce((SELECT sum(octet_length(content))::bigint FROM knowledge_items), 0)
  + coalesce((SELECT sum(octet_length(coalesce(extracted_text, '')))::bigint FROM attachments), 0)`

// Usage runs the FR-M11-05 metered aggregate under the tx's tenant scope. tx MUST come from
// store.WithTenant; require_tenant() runs first so a scopeless tx FAILS (never a silent empty
// that would meter a tenant at 0 — a billing-integrity hazard), ADR-0015 / FR-M11-01.
func Usage(ctx context.Context, tx pgx.Tx, w Window, now time.Time) (UsageReport, error) {
	// Isolation guard (ADR-0015): a missing scope must FAIL loudly here, not meter at zero.
	var scope string
	if err := tx.QueryRow(ctx, `SELECT require_tenant()`).Scan(&scope); err != nil {
		return UsageReport{}, fmt.Errorf("analytics: usage tenant scope required: %w", err)
	}

	var c UsageCounts
	var latest *time.Time
	if err := tx.QueryRow(ctx, usageWindowSQL, w.From, w.To).Scan(
		&c.Conversations, &c.Messages, &c.AutoSends, &latest); err != nil {
		return UsageReport{}, fmt.Errorf("analytics: usage window query: %w", err)
	}
	if err := tx.QueryRow(ctx, usageStorageSQL).Scan(&c.StorageBytes); err != nil {
		return UsageReport{}, fmt.Errorf("analytics: usage storage query: %w", err)
	}
	if latest != nil {
		c.HasRows = true
		c.Latest = *latest
	}
	return computeUsage(c, now), nil
}
