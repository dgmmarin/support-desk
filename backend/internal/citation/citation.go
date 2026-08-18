// Package citation defines the machine-resolvable citation that binds a factual
// claim in a draft to the exact source it is grounded in (FR-M5-02, ADR-0007). The
// point is programmatic resolution: a verifier or the agent console can map
// claim→source by id, not parse a prose "[1]" marker. A citation whose source is
// absent from the retrieved context set does not resolve — its claim is ungrounded
// and must be marked partial (FR-M5-03), never asserted as fact.
package citation

// Citation binds one claim span to the exact source it rests on. Exactly one of
// KnowledgeItemID (a retrieved chunk id) or BookingFieldPath (a system-of-record
// field path) identifies the source; Score carries the retrieval score of the cited
// chunk (0 for SoR fields). This is the shape the verifier and console resolve
// against (spec §3: claimSpan -> {knowledgeItemId|bookingFieldPath, score}).
type Citation struct {
	ClaimSpan        string  `json:"claim_span"`
	KnowledgeItemID  string  `json:"knowledge_item_id,omitempty"`
	BookingFieldPath string  `json:"booking_field_path,omitempty"`
	Score            float64 `json:"score,omitempty"`
}

// Resolves reports whether the citation points at a source the resolver can reach: a
// knowledge item present in sourceIDs (the retrieved context set), or a
// system-of-record field path. A citation whose knowledge id is absent from the set —
// or that names no source at all — does not resolve. Fail-closed by construction: the
// caller treats a non-resolving claim as ungrounded, never as grounded fact.
func (c Citation) Resolves(sourceIDs map[string]struct{}) bool {
	if c.BookingFieldPath != "" {
		return true // SoR-sourced; resolved by field path, not the chunk set
	}
	if c.KnowledgeItemID == "" {
		return false
	}
	_, ok := sourceIDs[c.KnowledgeItemID]
	return ok
}

// AllResolve reports whether every citation resolves against sourceIDs. An empty set
// of citations trivially resolves; one non-resolving citation fails the whole set, so
// an unresolved claim blocks auto-send upstream (FR-M5-02 fail-closed).
func AllResolve(cites []Citation, sourceIDs map[string]struct{}) bool {
	for _, c := range cites {
		if !c.Resolves(sourceIDs) {
			return false
		}
	}
	return true
}

// SourceSet builds a lookup set of source ids (the retrieved chunk ids) to resolve
// citations against.
func SourceSet(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}
