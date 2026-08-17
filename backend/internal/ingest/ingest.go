package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Outcome is the disposition of one ingested message. Every input maps to exactly
// one outcome — nothing is dropped (M1 §7 lossless invariant).
type Outcome string

const (
	Ingested    Outcome = "ingested"
	Duplicate   Outcome = "duplicate"
	Quarantined Outcome = "quarantined"
)

// Result is the ingest decision for one message.
type Result struct {
	Message          NormalisedMessage
	ConversationID   string
	Outcome          Outcome
	Automated        bool
	SuppressAutoSend bool   // loop cap reached for the sender (FR-M1-06)
	DuplicateOf      string // message id this duplicates, when Outcome == Duplicate
	QuarantineReason string
}

// fallbackWindow bounds the header-absent threading heuristic (FR-M1-05).
// ponytail: fixed 7d; the real window is tuned against an archive (M1 §8).
const fallbackWindow = 7 * 24 * time.Hour

// autoReplyBlockAfter blocks auto-send once this many auto-replies from an
// address have been seen (FR-M1-06: block after the 2nd inbound auto-reply).
const autoReplyBlockAfter = 2

type conversation struct {
	id           string
	subject      string // normalised
	participants map[string]bool
	lastActivity time.Time
}

// Ingestor holds the in-memory threading/dedup/loop state for a stream of
// messages. It is safe for concurrent use. State is per-instance; the DB-backed
// version (a follow-up issue) will persist the same decisions.
type Ingestor struct {
	now func() time.Time

	mu             sync.Mutex
	seq            int
	convByParent   map[string]*conversation // message-id → conversation
	conversations  []*conversation
	seenMessageIDs map[string]bool
	seenBodyHashes map[string]bool
	autoReplyCount map[string]int // sender email → count of auto-replies seen
}

// New returns an Ingestor. If now is nil, time.Now is used.
func New(now func() time.Time) *Ingestor {
	if now == nil {
		now = time.Now
	}
	return &Ingestor{
		now:            now,
		convByParent:   map[string]*conversation{},
		seenMessageIDs: map[string]bool{},
		seenBodyHashes: map[string]bool{},
		autoReplyCount: map[string]int{},
	}
}

// Process parses and dispositions one raw message.
func (in *Ingestor) Process(raw []byte) Result {
	msg, err := Parse(raw)
	if err != nil {
		// FR-M1-04: never lose a message to a parse failure — quarantine it.
		return Result{Outcome: Quarantined, QuarantineReason: err.Error()}
	}

	in.mu.Lock()
	defer in.mu.Unlock()

	// Deduplicate before threading so a resend never creates a new conversation.
	if msg.MessageID != "" && in.seenMessageIDs[msg.MessageID] {
		return Result{Message: msg, Outcome: Duplicate, DuplicateOf: msg.MessageID, Automated: msg.Automated}
	}
	bodyHash := hashBody(msg)
	if in.seenBodyHashes[bodyHash] {
		return Result{Message: msg, Outcome: Duplicate, DuplicateOf: msg.MessageID, Automated: msg.Automated}
	}

	conv := in.thread(msg)

	// Record state.
	if msg.MessageID != "" {
		in.seenMessageIDs[msg.MessageID] = true
		in.convByParent[msg.MessageID] = conv
	}
	in.seenBodyHashes[bodyHash] = true
	conv.lastActivity = laterTime(conv.lastActivity, in.effectiveTime(msg))
	for p := range msg.Participants() {
		conv.participants[p] = true
	}

	res := Result{Message: msg, ConversationID: conv.id, Outcome: Ingested, Automated: msg.Automated}

	// Loop cap: block auto-send once this sender has auto-replied too many times.
	if msg.Automated && msg.From.Email != "" {
		key := strings.ToLower(msg.From.Email)
		prior := in.autoReplyCount[key]
		if prior >= autoReplyBlockAfter {
			res.SuppressAutoSend = true
		}
		in.autoReplyCount[key] = prior + 1
	}
	return res
}

// thread finds the conversation for msg: header chain first, then the fallback
// heuristic, else a new conversation (erring toward new to avoid cross-customer
// mixing — FR-M1-05).
func (in *Ingestor) thread(msg NormalisedMessage) *conversation {
	for _, parent := range parentCandidates(msg) {
		if c := in.convByParent[parent]; c != nil {
			return c
		}
	}
	if c := in.fallbackConversation(msg); c != nil {
		return c
	}
	in.seq++
	c := &conversation{
		id:           fmt.Sprintf("conv-%d", in.seq),
		subject:      normaliseSubject(msg.Subject),
		participants: map[string]bool{},
	}
	in.conversations = append(in.conversations, c)
	return c
}

func (in *Ingestor) fallbackConversation(msg NormalisedMessage) *conversation {
	subj := normaliseSubject(msg.Subject)
	if subj == "" {
		return nil
	}
	parts := msg.Participants()
	t := in.effectiveTime(msg)
	for _, c := range in.conversations {
		if c.subject != subj {
			continue
		}
		if !sharesParticipant(c.participants, parts) {
			continue
		}
		if absDuration(t.Sub(c.lastActivity)) > fallbackWindow {
			continue
		}
		return c
	}
	return nil
}

func (in *Ingestor) effectiveTime(msg NormalisedMessage) time.Time {
	if msg.Date.IsZero() {
		return in.now()
	}
	return msg.Date
}

func parentCandidates(msg NormalisedMessage) []string {
	var out []string
	if msg.InReplyTo != "" {
		out = append(out, msg.InReplyTo)
	}
	// References list, most recent (last) first.
	for i := len(msg.References) - 1; i >= 0; i-- {
		out = append(out, msg.References[i])
	}
	return out
}

func hashBody(msg NormalisedMessage) string {
	h := sha256.Sum256([]byte(strings.ToLower(msg.From.Email) + "\x00" + normaliseSubject(msg.Subject) + "\x00" + msg.Text))
	return hex.EncodeToString(h[:])
}

func sharesParticipant(a, b map[string]bool) bool {
	for k := range b {
		if a[k] {
			return true
		}
	}
	return false
}

func normaliseSubject(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	for {
		trimmed := false
		for _, p := range []string{"re:", "fwd:", "fw:", "aw:"} {
			if strings.HasPrefix(s, p) {
				s = strings.TrimSpace(s[len(p):])
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
