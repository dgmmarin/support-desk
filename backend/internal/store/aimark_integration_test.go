//go:build integration

package store_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_FR_M13_02_ai_message_mark_persist_and_resolve — the immutable per-message
// model/version log is written and resolvable by SentMessage id, and the sent
// record carries the machine-readable AI marking.
func TestFRM1302AIMessageMarkPersistAndResolve(t *testing.T) {
	ctx, app := setupPersist(t)

	var sentID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "system",
			DisclosureText: "AI-assisted.", AIGenerated: true, DeliveryStatus: "sent",
		})
		if e != nil {
			return e
		}
		_, e = store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
			SentMessageID: sentID, AIGenerated: true, DisclosureMode: store.DisclosureModeAIGenerated,
			DisclosureText: "AI-assisted.", Model: "generate-model", ModelVersion: "2026-05-01",
			PromptVersion: "gen-v3", GeneratedAt: time.Now(),
		})
		return e
	}); err != nil {
		t.Fatalf("persist mark: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		m, found, e := store.GetAIMessageMark(ctx, tx, sentID)
		if e != nil {
			return e
		}
		if !found {
			t.Fatal("mark not resolvable for sent message (FR-M13-02)")
		}
		if !m.AIGenerated || m.Model != "generate-model" || m.ModelVersion != "2026-05-01" {
			t.Fatalf("mark = %+v, want ai+model+version recorded", m)
		}
		if m.DisclosureMode != store.DisclosureModeAIGenerated || m.DisclosureText != "AI-assisted." {
			t.Fatalf("disclosure not logged per message (FR-M13-01): %+v", m)
		}
		return nil
	}); err != nil {
		t.Fatalf("resolve mark: %v", err)
	}
}

// test_FR_M13_02_sent_message_carries_machine_readable_marking — sent_messages.ai_generated
// round-trips as the machine-readable AI marking on the message.
func TestFRM1302SentMessageCarriesMachineReadableMarking(t *testing.T) {
	ctx, app := setupPersist(t)

	var draftID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		draftID, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		_, _, e = store.InsertSentMessageOnce(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "system",
			DisclosureText: "AI-assisted.", AIGenerated: true, DeliveryStatus: "sent",
		})
		return e
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		sm, found, e := store.GetSentMessageByDraft(ctx, tx, draftID)
		if e != nil {
			return e
		}
		if !found || !sm.AIGenerated {
			t.Fatalf("sent message must carry machine-readable AI marking (FR-M13-02), got %+v", sm)
		}
		return nil
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
}

// test_FR_M13_02_ai_message_mark_requires_model_version — fail-closed: an
// AI-generated mark with no model/version is refused (no human-oversight evidence
// ⇒ not sendable).
func TestFRM1302AIMessageMarkRequiresModelVersion(t *testing.T) {
	ctx, app := setupPersist(t)

	err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		sentID, e := store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "system",
			DisclosureText: "AI-assisted.", AIGenerated: true, DeliveryStatus: "sent",
		})
		if e != nil {
			return e
		}
		_, e = store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
			SentMessageID: sentID, AIGenerated: true, DisclosureMode: store.DisclosureModeAIGenerated,
			DisclosureText: "AI-assisted.", Model: "", ModelVersion: "", GeneratedAt: time.Now(),
		})
		return e
	})
	if err == nil {
		t.Fatal("AI-generated mark without model/version must be refused (FR-M13-02 fail-closed)")
	}
}

// test_FR_M13_01_ai_generated_mark_requires_disclosure — fail-closed: an
// AI-generated mark with no disclosure text is refused (disclosure missing ⇒ not
// sendable).
func TestFRM1301AIGeneratedMarkRequiresDisclosure(t *testing.T) {
	ctx, app := setupPersist(t)

	err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		sentID, e := store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "system", AIGenerated: true, DeliveryStatus: "sent",
		})
		if e != nil {
			return e
		}
		_, e = store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
			SentMessageID: sentID, AIGenerated: true, DisclosureMode: store.DisclosureModeAIGenerated,
			DisclosureText: "  ", Model: "m", ModelVersion: "v1", GeneratedAt: time.Now(),
		})
		return e
	})
	if err == nil {
		t.Fatal("AI-generated mark without disclosure must be refused (FR-M13-01 fail-closed)")
	}
}

// test_LEG_08_human_reviewed_mark_recorded_distinctly — a human-owned message
// records a distinct disclosure_mode and is not AI-marked; model/version is not
// mandatory for it (ADR-0024 / LEG-08).
func TestLEG08HumanReviewedMarkRecordedDistinctly(t *testing.T) {
	ctx, app := setupPersist(t)

	var sentID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "agent-7", AIGenerated: false, DeliveryStatus: "sent",
		})
		if e != nil {
			return e
		}
		_, e = store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
			SentMessageID: sentID, AIGenerated: false, DisclosureMode: store.DisclosureModeHumanReviewed,
			GeneratedAt: time.Now(),
		})
		return e
	}); err != nil {
		t.Fatalf("human-reviewed mark must be allowed without model/version (LEG-08): %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		m, found, e := store.GetAIMessageMark(ctx, tx, sentID)
		if e != nil {
			return e
		}
		if !found || m.AIGenerated || m.DisclosureMode != store.DisclosureModeHumanReviewed {
			t.Fatalf("human-owned message must record a distinct, non-AI marking (LEG-08), got %+v", m)
		}
		return nil
	}); err != nil {
		t.Fatalf("resolve human-reviewed mark: %v", err)
	}
}

// test_INV_2_ai_message_mark_immutable — the per-message log is append-only.
func TestINV2AIMessageMarkImmutable(t *testing.T) {
	ctx, app := setupPersist(t)

	var id string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		sentID, e := store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "system",
			DisclosureText: "AI-assisted.", AIGenerated: true, DeliveryStatus: "sent",
		})
		if e != nil {
			return e
		}
		id, e = store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
			SentMessageID: sentID, AIGenerated: true, DisclosureMode: store.DisclosureModeAIGenerated,
			DisclosureText: "AI-assisted.", Model: "m", ModelVersion: "v1", GeneratedAt: time.Now(),
		})
		return e
	}); err != nil {
		t.Fatalf("insert mark: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE ai_message_marks SET model='tamper' WHERE id=$1", id)
		return e
	}); err == nil {
		t.Fatal("UPDATE on ai_message_marks must be rejected (INV-2)")
	}
}

// test_INV_1_ai_message_mark_tenant_isolated — tenant B cannot read tenant A's mark.
func TestINV1AIMessageMarkTenantIsolated(t *testing.T) {
	ctx, app := setupPersist(t)

	var sentID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "system",
			DisclosureText: "AI-assisted.", AIGenerated: true, DeliveryStatus: "sent",
		})
		if e != nil {
			return e
		}
		_, e = store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
			SentMessageID: sentID, AIGenerated: true, DisclosureMode: store.DisclosureModeAIGenerated,
			DisclosureText: "AI-assisted.", Model: "m", ModelVersion: "v1", GeneratedAt: time.Now(),
		})
		return e
	}); err != nil {
		t.Fatalf("seed A mark: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		_, found, e := store.GetAIMessageMark(ctx, tx, sentID)
		if e != nil {
			return e
		}
		if found {
			t.Fatal("tenant B must not resolve tenant A's AI message mark (INV-1)")
		}
		return nil
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
}
