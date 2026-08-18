package mailprovider

import (
	"encoding/json"
	"testing"

	"tourdesk/internal/pipeline"
)

// test_FR_M1_03_ingest_envelope_is_tenant_scoped_raw — the inbound bridge builds an
// ingest envelope that (a) carries the raw MIME under the ingest "raw" shape and
// (b) is scoped to the mailbox's tenant (data-layer scope, ADR-0015).
func TestFRM103IngestEnvelopeTenantScopedRaw(t *testing.T) {
	raw := []byte("From: cust@x.com\r\nSubject: Hi\r\n\r\nBody")
	b, err := IngestEnvelope(Mailbox{TenantID: "tenant-a", MailboxID: "mb1"}, "corr-1", raw)
	if err != nil {
		t.Fatalf("IngestEnvelope: %v", err)
	}
	var env pipeline.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.TenantID != "tenant-a" {
		t.Fatalf("TenantID = %q, want tenant-a (ADR-0015 scope)", env.TenantID)
	}
	if env.CorrelationID != "corr-1" {
		t.Fatalf("CorrelationID = %q, want corr-1", env.CorrelationID)
	}
	var p ingestPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if string(p.Raw) != string(raw) {
		t.Fatalf("raw round-trip mismatch: %q", string(p.Raw))
	}
}
