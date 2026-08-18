package citation

import "testing"

// test_FR_M5_02_resolver_maps_claim_to_source — a citation naming a retrieved chunk
// id resolves against that set; a citation naming an absent id does not (fail-closed).
func TestResolvesAgainstRetrievedSet(t *testing.T) {
	set := SourceSet("k1", "k2")
	if !(Citation{ClaimSpan: "baggage 20kg", KnowledgeItemID: "k1"}).Resolves(set) {
		t.Fatal("a citation to a retrieved chunk id must resolve (FR-M5-02)")
	}
	if (Citation{ClaimSpan: "refunds 30 days", KnowledgeItemID: "ghost"}).Resolves(set) {
		t.Fatal("a citation to an id absent from the retrieved set must NOT resolve (fail-closed)")
	}
	if (Citation{ClaimSpan: "uncited"}).Resolves(set) {
		t.Fatal("a citation with no source must not resolve")
	}
}

// test_FR_M5_02_booking_field_citation_resolves — a system-of-record field-path
// citation is machine-resolvable by its path, independent of the chunk set.
func TestBookingFieldCitationResolves(t *testing.T) {
	if !(Citation{ClaimSpan: "departs 09:00", BookingFieldPath: "booking.segments[0].departure"}).Resolves(SourceSet()) {
		t.Fatal("a SoR field-path citation must be resolvable by its path")
	}
}

// test_FR_M5_02_all_resolve — AllResolve is true only when every citation resolves;
// one unresolved citation fails the set (blocks auto-send upstream).
func TestAllResolve(t *testing.T) {
	set := SourceSet("k1")
	ok := []Citation{{KnowledgeItemID: "k1"}}
	if !AllResolve(ok, set) {
		t.Fatal("all-resolving citations must pass AllResolve")
	}
	bad := []Citation{{KnowledgeItemID: "k1"}, {KnowledgeItemID: "ghost"}}
	if AllResolve(bad, set) {
		t.Fatal("one unresolved citation must fail AllResolve (fail-closed)")
	}
	if !AllResolve(nil, set) {
		t.Fatal("no citations trivially resolve")
	}
}
