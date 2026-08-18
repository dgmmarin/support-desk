package knowledgeindex

import (
	"context"
	"hash/fnv"
	"strings"
)

// EmbedDim is the fixed embedding dimension of the stub embedder.
const EmbedDim = 16

// HashEmbedder is a deterministic, dependency-free stand-in for a real embedding
// model. It projects the chunk's terms into a fixed-width bag-of-words vector so
// the same text always yields the same vector (reproducible, replay-safe).
//
// ponytail: this is NOT a semantic model — the point of ISSUE-0047 is the
// chunk+metadata+index-write + isolation contract, not embedding quality. Upgrade
// path: swap for the pinned provider embedding behind the same Embedder seam
// (ADR-0010), keeping the persisted vector column.
type HashEmbedder struct{}

var _ Embedder = HashEmbedder{}

// Embed returns a deterministic EmbedDim-length vector of term-bucket frequencies.
func (HashEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, EmbedDim)
	for _, term := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(term))
		vec[h.Sum32()%EmbedDim]++
	}
	return vec, nil
}
