-- 0026 — M13 automated retention deletion (FR-M13-05 / spec §3 / §11.4), tenant-scoped
-- (ADR-0015). Idempotent.
--
-- Deletes data past its per-data-class retention window. The Go layer (SweepRetention)
-- resolves each tenant's windows (config + platform defaults) into concrete cutoff
-- instants and calls this function; the function performs the one governed deletion.
--
-- INV-2 append-only: an expired conversation's descendants include append-only tables
-- (messages, gate_evaluations, sent_messages, ai_message_marks, review_actions,
-- case_notes, case_note_mentions) carrying the deny_mutation() trigger. INV-2 forbids
-- app-level tampering, NOT a lawful, audited retention deletion — so, exactly like the
-- DSAR erasure (migration 0025), this ONE governed operation runs as a migration-owned
-- SECURITY DEFINER function that may bypass the trigger for its own statements only. The
-- global trigger is NOT weakened.
--
-- Mechanism: under session_replication_role='replica' BOTH the deny_mutation triggers
-- AND the ON DELETE CASCADE / FK-check triggers are disabled (they are origin-fired). So
-- we delete the append-only descendants EXPLICITLY under replica, then switch back to
-- 'origin' and delete the parent conversations — cascade then removes the remaining
-- MUTABLE descendants (drafts, complaints, case_queue, crisis_event_*, promotion refs,
-- …), and the append-only descendants are already gone so their per-row triggers fire
-- on no rows. Every append-only table that is a descendant of a conversation MUST be
-- listed in the replica block below; a missing one surfaces as a deny_mutation error in
-- the retention test.
--
-- Tenant safety: SECURITY DEFINER runs as the superuser owner (which BYPASSES RLS), so
-- the function derives the tenant from cur_tenant() and filters EVERY statement by it. It
-- takes NO tenant parameter — a caller can only ever delete within the tenant it is
-- already scoped to (ADR-0015; retention never spans tenants). A NULL scope fails closed.
--
-- audit_records and telemetry_events are RETAINED (held) under FR-M13-10 / retention §3
-- and are never touched here.
CREATE OR REPLACE FUNCTION retention_delete_expired(
    p_conversation_cutoff timestamptz,
    p_attachment_cutoff   timestamptz)
  RETURNS TABLE(store text, affected bigint)
  LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
  AS $$
DECLARE
  v_tenant uuid := cur_tenant();
  v_convs  uuid[];
  v_sent   uuid[];
  n        bigint;
BEGIN
  IF v_tenant IS NULL THEN
    RAISE EXCEPTION 'retention_delete_expired requires a tenant scope (fail-closed)';
  END IF;

  -- Conversations idle past the conversation-class window (keyed on last activity).
  SELECT coalesce(array_agg(id), ARRAY[]::uuid[]) INTO v_convs
    FROM conversations
   WHERE tenant_id = v_tenant AND last_activity_at <= p_conversation_cutoff;

  SELECT coalesce(array_agg(id), ARRAY[]::uuid[]) INTO v_sent
    FROM sent_messages
   WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);

  -- Governed carve-out: disable deny_mutation + cascade/FK triggers for THIS operation.
  SET LOCAL session_replication_role = 'replica';

  -- (1) Attachment retention class (independent, shorter window): expired attachments on
  --     ANY conversation. Mutable table; deleted here on the one governed path.
  DELETE FROM attachments
   WHERE tenant_id = v_tenant AND created_at <= p_attachment_cutoff;
  GET DIAGNOSTICS n = ROW_COUNT; store := 'attachments'; affected := n; RETURN NEXT;

  -- (2) Append-only descendants of an expired conversation — deleted explicitly under
  --     replica (cascade + deny_mutation are off), grandchildren before children.
  DELETE FROM case_note_mentions WHERE tenant_id = v_tenant
     AND note_id IN (SELECT id FROM case_notes WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs));
  DELETE FROM case_notes         WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);
  DELETE FROM ai_message_marks   WHERE tenant_id = v_tenant AND sent_message_id = ANY(v_sent);
  DELETE FROM review_actions     WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);
  -- promotion_candidates.source_sent_id is ON DELETE SET NULL; sever it explicitly since
  -- the SET NULL trigger will not fire under replica (defends any cross-conversation ref).
  UPDATE promotion_candidates    SET source_sent_id = NULL
   WHERE tenant_id = v_tenant AND source_sent_id = ANY(v_sent);
  DELETE FROM sent_messages      WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);
  DELETE FROM gate_evaluations   WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);
  -- attachments on the expired convs' messages (may be newer than the attachment window,
  -- but the whole conversation is being deleted); their message parent goes next.
  DELETE FROM attachments        WHERE tenant_id = v_tenant
     AND message_id IN (SELECT id FROM messages WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs));
  DELETE FROM messages           WHERE tenant_id = v_tenant AND conversation_id = ANY(v_convs);

  -- Back to origin: cascade removes the remaining MUTABLE descendants as the parent goes.
  SET LOCAL session_replication_role = 'origin';

  DELETE FROM conversations WHERE tenant_id = v_tenant AND id = ANY(v_convs);
  GET DIAGNOSTICS n = ROW_COUNT; store := 'conversations'; affected := n; RETURN NEXT;
END
$$;

REVOKE ALL ON FUNCTION retention_delete_expired(timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION retention_delete_expired(timestamptz, timestamptz) TO tourdesk_app;
