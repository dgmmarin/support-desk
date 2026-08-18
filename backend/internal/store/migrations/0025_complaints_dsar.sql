-- 0025 — M13 complaint register (FR-M13-03 / LEG-13/14) + DSAR erasure function
-- (FR-M13-04 / LEG-05), tenant-scoped RLS (ADR-0015). Idempotent.

-- ── Complaint register (Package Travel Directive) ─────────────────────────────
-- One tracked record per complaint case: timestamp, assigned owner, response
-- deadline, tracked to a closure state, reportable (LEG-13). MUTABLE (owner is
-- assigned, status advances to 'closed') so no INV-2 immutability trigger — the
-- lawful audit of the case lives in audit_records. A complaint is NEVER
-- auto-answered: that is the G04 hard-stop path (M6), not enforced here.
CREATE TABLE IF NOT EXISTS complaints (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  complaint_type  text NOT NULL DEFAULT '',
  owner           text NOT NULL DEFAULT '',
  status          text NOT NULL DEFAULT 'open',      -- open | closed
  closure_reason  text NOT NULL DEFAULT '',
  registered_at   timestamptz NOT NULL DEFAULT now(),
  deadline        timestamptz NOT NULL,
  closed_at       timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, conversation_id)                -- one complaint per case (idempotent register)
);

CREATE INDEX IF NOT EXISTS complaints_open_idx ON complaints (tenant_id, status, deadline);

ALTER TABLE complaints ENABLE ROW LEVEL SECURITY;
ALTER TABLE complaints FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON complaints;
CREATE POLICY tenant_isolation ON complaints
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON complaints TO tourdesk_app;

-- ── DSAR erasure across stores (FR-M13-04 / LEG-05) ───────────────────────────
-- A GDPR right-to-erasure must reach every store holding the subject's personal
-- data. Two stores are append-only (INV-2): messages and sent_messages carry the
-- deny_mutation() trigger. INV-2 forbids app-level tampering, NOT a lawful, audited
-- erasure — so this ONE governed operation runs as a migration-owned SECURITY
-- DEFINER function that can skip the trigger for its own statements only.
--
-- Tenant safety: the function is SECURITY DEFINER (runs as the superuser owner, which
-- BYPASSES RLS), so it derives the tenant from cur_tenant() and filters EVERY
-- statement by it. It takes NO tenant parameter — a caller can only ever erase the
-- tenant it is already scoped to (ADR-0015; a DSAR never spans tenants).
--
-- audit_records and telemetry_events are NOT touched here: they are RETAINED under a
-- documented legal-hold / non-identifying basis (FR-M13-10, LEG-05, retention §3).
CREATE OR REPLACE FUNCTION dsar_erase_content(p_subject text)
  RETURNS TABLE(store text, affected bigint)
  LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
  AS $$
DECLARE
  v_tenant uuid := cur_tenant();
  v_convs  uuid[];
  n        bigint;
BEGIN
  IF v_tenant IS NULL THEN
    RAISE EXCEPTION 'dsar_erase_content requires a tenant scope (fail-closed)';
  END IF;

  SELECT coalesce(array_agg(id), ARRAY[]::uuid[]) INTO v_convs
    FROM conversations WHERE tenant_id = v_tenant AND customer_email = p_subject;

  -- Governed erasure skips the append-only trigger (INV-2) for THIS operation only.
  SET LOCAL session_replication_role = 'replica';

  -- Immutable content-bearing stores: crypto-erase/redact personal columns in place,
  -- preserving the row so the decision structure (INV-5) still reconstructs.
  UPDATE messages
     SET body = '[erased:dsar]', from_addr = '[erased:dsar]', subject = '[erased:dsar]'
   WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);
  GET DIAGNOSTICS n = ROW_COUNT; store := 'messages'; affected := n; RETURN NEXT;

  UPDATE sent_messages
     SET content = '[erased:dsar]', sender = '[erased:dsar]', disclosure_text = NULL
   WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);
  GET DIAGNOSTICS n = ROW_COUNT; store := 'sent_messages'; affected := n; RETURN NEXT;

  -- Mutable stores: redact personal content directly.
  UPDATE drafts SET content = '[erased:dsar]'
   WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);
  GET DIAGNOSTICS n = ROW_COUNT; store := 'drafts'; affected := n; RETURN NEXT;

  UPDATE attachments SET extracted_text = '[erased:dsar]', filename = '[erased:dsar]'
   WHERE tenant_id = v_tenant
     AND message_id IN (SELECT id FROM messages WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs));
  GET DIAGNOSTICS n = ROW_COUNT; store := 'attachments'; affected := n; RETURN NEXT;

  -- The shared index must hold NO personal data (FR-M4-13); redact any hit as a
  -- safety net (expected 0) so the tooling provably reaches the index.
  UPDATE knowledge_items SET content = '[erased:dsar]'
   WHERE tenant_id = v_tenant AND content ILIKE '%' || p_subject || '%';
  GET DIAGNOSTICS n = ROW_COUNT; store := 'knowledge_items'; affected := n; RETURN NEXT;

  -- Finally sever the subject linkage so a re-export by email finds nothing.
  UPDATE conversations SET customer_email = NULL, subject = '[erased:dsar]'
   WHERE tenant_id = v_tenant AND id = ANY(v_convs);
  GET DIAGNOSTICS n = ROW_COUNT; store := 'conversations'; affected := n; RETURN NEXT;
END
$$;

REVOKE ALL ON FUNCTION dsar_erase_content(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION dsar_erase_content(text) TO tourdesk_app;
