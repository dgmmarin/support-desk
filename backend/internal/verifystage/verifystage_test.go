package verifystage

import (
	"testing"

	"tourdesk/internal/citation"
	"tourdesk/internal/verify"
)

// test_FR_M5_02_verify_reads_machine_resolvable_citations — gateReady is the
// deterministic consumption of the machine-resolvable citation shape (ADR-0007,
// independent of the model): a passing model verdict proceeds to the gate only when
// every carried citation resolves against the retrieved source ids.
func TestGateReadyResolvesCitations(t *testing.T) {
	pass := verify.Verdict{PerClaim: []verify.ClaimVerdict{{ClaimSpan: "x", Supported: true}}}

	// Model passes and all citations resolve → ready for the gate.
	if !gateReady(pass, []citation.Citation{{ClaimSpan: "x", KnowledgeItemID: "k1"}}, []string{"k1"}) {
		t.Fatal("a passing verdict with resolving citations must be gate-ready")
	}
	// Model passes but a citation names an id absent from the sources → fail-closed.
	if gateReady(pass, []citation.Citation{{ClaimSpan: "x", KnowledgeItemID: "ghost"}}, []string{"k1"}) {
		t.Fatal("an unresolved citation must block auto-send even on a passing model verdict (FR-M5-02)")
	}
	// Model fails → never gate-ready regardless of citations.
	fail := verify.Verdict{Flags: verify.Flags{Unsupported: true}}
	if gateReady(fail, nil, nil) {
		t.Fatal("a failing model verdict must never be gate-ready")
	}
	// No citations trivially resolve; a passing verdict proceeds.
	if !gateReady(pass, nil, nil) {
		t.Fatal("a passing verdict with no citations must be gate-ready")
	}
}
