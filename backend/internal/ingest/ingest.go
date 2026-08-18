package ingest

import (
	"context"
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
	SuppressAutoSend bool        // loop cap reached for the sender (FR-M1-06)
	BounceClass      BounceClass // hard/soft/unknown when this is a DSN (FR-M1-07)
	BounceRecipient  string      // the failed recipient carried by the DSN (FR-M1-07)
	DuplicateOf      string      // message id this duplicates, when Outcome == Duplicate
	QuarantineReason string
}

// FallbackWindow bounds the header-absent threading heuristic (FR-M1-05).
// ponytail: fixed 7d; the real window is tuned against an archive (M1 §8).
const FallbackWindow = 7 * 24 * time.Hour

// autoReplyBlockAfter blocks auto-send once this many auto-replies from an
// address have been seen (FR-M1-06: block after the 2nd inbound auto-reply).
const autoReplyBlockAfter = 2

// Repo is the threading/dedup/loop-cap state the orchestration operates over.
// Implementations: memRepo (in-memory) and store.IngestRepo (Postgres, tenant-
// scoped). Keeping the orchestration in Process means the safety-critical ordering
// lives in one place regardless of backend.
type Repo interface {
	// Duplicate reports whether a message with this id or body hash was already ingested.
	Duplicate(ctx context.Context, messageID, bodyHash string) (bool, error)
	// FindConversation returns an existing conversation id via header chain
	// (parents) then fallback (subjectNorm + participant + window), or "" if none.
	FindConversation(ctx context.Context, parents []string, subjectNorm string, participants []string, t time.Time) (string, error)
	// CreateConversation creates a new conversation and returns its id.
	CreateConversation(ctx context.Context, subjectNorm string, participants []string, t time.Time) (string, error)
	// AutoReplyCount returns prior auto-replies from fromEmail within the loop window.
	AutoReplyCount(ctx context.Context, fromEmail string, t time.Time) (int, error)
	// RecordMessage persists the message under conversationID and updates state.
	RecordMessage(ctx context.Context, conversationID, bodyHash string, msg NormalisedMessage, t time.Time) error
}

// Process parses and dispositions one raw message against repo. Deterministic
// given repo + now. The ordering is the contract: dedup → thread (header chain,
// then fallback) → record → loop-cap; a parse failure quarantines (never drops).
func Process(ctx context.Context, repo Repo, raw []byte, now time.Time) (Result, error) {
	msg, err := Parse(raw)
	if err != nil {
		return Result{Outcome: Quarantined, QuarantineReason: err.Error()}, nil
	}
	bodyHash := hashBody(msg)

	dup, err := repo.Duplicate(ctx, msg.MessageID, bodyHash)
	if err != nil {
		return Result{}, err
	}
	if dup {
		return Result{Message: msg, Outcome: Duplicate, DuplicateOf: msg.MessageID, Automated: msg.Automated}, nil
	}

	t := effectiveTime(msg, now)
	parents := parentCandidates(msg)
	participants := participantList(msg)
	subjectNorm := normaliseSubject(msg.Subject)

	convID, err := repo.FindConversation(ctx, parents, subjectNorm, participants, t)
	if err != nil {
		return Result{}, err
	}
	if convID == "" {
		if convID, err = repo.CreateConversation(ctx, subjectNorm, participants, t); err != nil {
			return Result{}, err
		}
	}

	res := Result{Message: msg, ConversationID: convID, Outcome: Ingested, Automated: msg.Automated,
		BounceClass: msg.BounceClass, BounceRecipient: msg.BounceRecipient}

	if msg.Automated && msg.From.Email != "" {
		// Count prior auto-replies BEFORE recording this one, so the cap blocks
		// from the (autoReplyBlockAfter+1)-th message.
		prior, err := repo.AutoReplyCount(ctx, strings.ToLower(msg.From.Email), t)
		if err != nil {
			return Result{}, err
		}
		if prior >= autoReplyBlockAfter {
			res.SuppressAutoSend = true
		}
	}

	if err := repo.RecordMessage(ctx, convID, bodyHash, msg, t); err != nil {
		return Result{}, err
	}
	return res, nil
}

// Ingestor is the in-memory convenience wrapper (used by unit tests and the
// nil-store stage): it holds a memRepo and a clock and exposes the simple
// Process(raw) API.
type Ingestor struct {
	repo *memRepo
	now  func() time.Time
}

// New returns an in-memory Ingestor. If now is nil, time.Now is used.
func New(now func() time.Time) *Ingestor {
	if now == nil {
		now = time.Now
	}
	return &Ingestor{repo: newMemRepo(), now: now}
}

// Process dispositions one raw message against the in-memory state.
func (in *Ingestor) Process(raw []byte) Result {
	r, _ := Process(context.Background(), in.repo, raw, in.now())
	return r
}

// --- in-memory Repo ---

type conversation struct {
	id           string
	subject      string
	participants map[string]bool
	lastActivity time.Time
}

type memRepo struct {
	mu             sync.Mutex
	seq            int
	convByID       map[string]*conversation
	convByParent   map[string]*conversation
	conversations  []*conversation
	seenMessageIDs map[string]bool
	seenBodyHashes map[string]bool
	autoReplyCount map[string]int
}

func newMemRepo() *memRepo {
	return &memRepo{
		convByID:       map[string]*conversation{},
		convByParent:   map[string]*conversation{},
		seenMessageIDs: map[string]bool{},
		seenBodyHashes: map[string]bool{},
		autoReplyCount: map[string]int{},
	}
}

func (m *memRepo) Duplicate(_ context.Context, messageID, bodyHash string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if messageID != "" && m.seenMessageIDs[messageID] {
		return true, nil
	}
	return m.seenBodyHashes[bodyHash], nil
}

func (m *memRepo) FindConversation(_ context.Context, parents []string, subjectNorm string, participants []string, t time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range parents {
		if c := m.convByParent[p]; c != nil {
			return c.id, nil
		}
	}
	if subjectNorm == "" {
		return "", nil
	}
	pset := toSet(participants)
	for _, c := range m.conversations {
		if c.subject != subjectNorm || !sharesParticipant(c.participants, pset) {
			continue
		}
		if absDuration(t.Sub(c.lastActivity)) > FallbackWindow {
			continue
		}
		return c.id, nil
	}
	return "", nil
}

func (m *memRepo) CreateConversation(_ context.Context, subjectNorm string, participants []string, t time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	c := &conversation{
		id:           fmt.Sprintf("conv-%d", m.seq),
		subject:      subjectNorm,
		participants: toSet(participants),
		lastActivity: t,
	}
	m.convByID[c.id] = c
	m.conversations = append(m.conversations, c)
	return c.id, nil
}

func (m *memRepo) AutoReplyCount(_ context.Context, fromEmail string, _ time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.autoReplyCount[fromEmail], nil
}

func (m *memRepo) RecordMessage(_ context.Context, conversationID, bodyHash string, msg NormalisedMessage, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.convByID[conversationID]
	if c == nil {
		return fmt.Errorf("ingest: unknown conversation %q", conversationID)
	}
	if msg.MessageID != "" {
		m.seenMessageIDs[msg.MessageID] = true
		m.convByParent[msg.MessageID] = c
	}
	m.seenBodyHashes[bodyHash] = true
	c.lastActivity = laterTime(c.lastActivity, t)
	for p := range msg.Participants() {
		c.participants[p] = true
	}
	if msg.Automated && msg.From.Email != "" {
		m.autoReplyCount[strings.ToLower(msg.From.Email)]++
	}
	return nil
}

// --- shared helpers ---

func participantList(msg NormalisedMessage) []string {
	seen := map[string]bool{}
	var out []string
	add := func(e string) {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	add(msg.From.Email) // From first — used as the conversation's customer email
	for _, a := range msg.To {
		add(a.Email)
	}
	for _, a := range msg.Cc {
		add(a.Email)
	}
	return out
}

func parentCandidates(msg NormalisedMessage) []string {
	var out []string
	if msg.InReplyTo != "" {
		out = append(out, msg.InReplyTo)
	}
	for i := len(msg.References) - 1; i >= 0; i-- {
		out = append(out, msg.References[i])
	}
	return out
}

func hashBody(msg NormalisedMessage) string {
	h := sha256.Sum256([]byte(strings.ToLower(msg.From.Email) + "\x00" + normaliseSubject(msg.Subject) + "\x00" + msg.Text))
	return hex.EncodeToString(h[:])
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, i := range items {
		s[strings.ToLower(i)] = true
	}
	return s
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

func effectiveTime(msg NormalisedMessage, now time.Time) time.Time {
	if msg.Date.IsZero() {
		return now
	}
	return msg.Date
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
