# M1 — Mail connectivity and conversation management — Specification

- **PRD module:** §7 M1
- **Depends on:** M11 (tenant/mailbox config, secrets vault), attachment scanning (SEC-07), M2 (auth results feed verification)
- **Consumed by:** M3 (normalised Message), M2 (candidate identifiers), M5/M7 (send), M13 (audit, deliverability evidence)

Related decisions: [ADR-0014 mail-provider abstraction and threading](../adr/0014-mail-provider-abstraction-and-threading.md),
[ADR-0016 content is data, not instructions](../adr/0016-content-is-data-not-instructions.md),
[ADR-0017 autonomy safety controls](../adr/0017-autonomy-safety-controls.md).

## 1. Purpose & scope

M1 owns the boundary between the outside mail world and the pipeline: it ingests mail from any of three
provider types behind one interface, parses it losslessly, threads messages into **Conversations**,
suppresses auto-responder loops and duplicates, records inbound authentication, handles attachments
safely, and sends threaded replies from the tenant's own domain. It is the only module that talks SMTP/IMAP/
Graph/Gmail. It does **not** classify or answer — it produces a clean, threaded, authenticated `Message`
and hands it to Stage 2 (Screen).

## 2. Requirements

| FR | Testable contract | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M1-01 | Connect over IMAP/SMTP, MS Graph or Gmail API through a single `MailProvider` interface; providers are swappable without touching pipeline code. | M | Connection failure → mailbox marked `degraded`, alert (FR-M11-06); no mail lost, retry with backoff. |
| FR-M1-02 | Support N mailboxes and N sending identities per tenant, each with own config, knowledge scope, policy. | M | Misrouted mailbox → quarantine, do not guess brand. |
| FR-M1-03 | New message enters pipeline within 60 s of arrival (poll or push); see NFR-P-01. | M | Lag > threshold → alert (NFR-R-03); messages still queued, never dropped. |
| FR-M1-04 | Parse MIME: text + HTML, inline images, quoted history, forwarded chains, non-UTF-8 charsets, malformed messages. Never lose a message to a parse failure. | M | Parse failure → **quarantine + alert**, replayable (NFR-R-02); never silent drop. |
| FR-M1-05 | Thread into Conversations via `Message-ID`/`In-Reply-To`/`References`, with a subject+participants+time-window fallback when headers absent. | M | Ambiguous threading → create new Conversation rather than misattach to wrong customer. |
| FR-M1-06 | Detect + suppress auto-responders/loops: honour `Auto-Submitted`, `X-Auto-Response-Suppress`, `Precedence: bulk/list`, vacation replies, bounces, DSNs. Hard cap: ≤3 automated messages/address/24 h; never reply to an address that has auto-replied twice. | M | Any loop signal → suppress auto-send, route to human/file; cap is enforced deterministically before send. |
| FR-M1-07 | Detect + classify bounces (hard/soft); mark case undeliverable. | M | Unknown DSN → treat as undeliverable, flag human; never mark "sent OK". |
| FR-M1-08 | Verify SPF/DKIM/DMARC on inbound; record result. Fail/absent blocks identity verification (M2) and blocks auto-send of any personal data. | M | Auth fail/absent → verification capped below `weak`; personal-data auto-send blocked (gate G08). |
| FR-M1-09 | Attachments: store securely, malware-scan (SEC-07), extract text from PDF/DOCX/images (OCR) for context, detect + mask sensitive PII (passport, card numbers) before prompts. | M | Scan fail/malware → do not extract, quarantine attachment, flag human; masking failure → do not pass raw to model. |
| FR-M1-10 | Send replies that thread in the customer's client (`In-Reply-To`, `References`, preserved subject), from tenant domain, with tenant signature/format. | M | Send API error → do not mark sent; retry idempotently (NFR-S-04); after N retries → human alert. |
| FR-M1-11 | Document + validate deliverability (SPF/DKIM/DMARC alignment for the sending domain) at onboarding; block go-live until it passes. | M | Validation fail → go-live blocked (hard gate in M11 onboarding). |
| FR-M1-12 | Deduplicate identical messages (same `Message-ID`, or sender+subject+body-hash within a window). | S | On uncertainty, keep both but link as suspected-dup; never silently drop a genuine message. |
| FR-M1-13 | Configurable hold-before-send delay (default 60 s) during which an auto-send can be cancelled by a supervisor or a newly-arrived same-thread message. | S | New inbound in thread during hold → cancel pending auto-send, re-queue. |
| FR-M1-14 | Support CC/BCC participants; detect multiple correspondents on a booking (traveller/purchaser/agent). | S | Multiple correspondents → do not assume identity; hand to M2. |
| FR-M1-15 | Import historical mail (sent+received, configurable window) at onboarding for tone/eval/knowledge mining. | S | Import in a mode where **send is physically impossible** (NFR-R-04). |
| FR-M1-16 | Optional shared-mailbox coexistence: leave messages in place, mark with labels/categories so the tenant's existing client still works. | C | If labelling unsupported by provider → disable coexistence for that mailbox, warn admin. |

**SR-M1-01 (spec addition).** All send paths are idempotent on a `(conversation_id, draft_id)` key so a
retry after an ambiguous SMTP result cannot double-send (satisfies NFR-S-04; a duplicate reply is a P1
defect). *Introduced here because the PRD states the invariant (NFR-S-04) but not the mechanism.*

## 3. Interfaces

```
interface MailProvider {                       // FR-M1-01, one impl per provider type
  watch(mailbox) -> stream<RawMessage>         // push (Graph/Gmail webhooks) or poll loop (IMAP)
  fetch(mailbox, uid) -> RawMessage
  send(sendingIdentity, OutboundMessage) -> {providerMessageId, delivery}   // idempotent, SR-M1-01
  labels?(mailbox, uid, labels)               // optional, FR-M1-16
}

// Emitted to Stage 2 (Screen):
event MailIngested {
  correlationId, tenantId, mailboxId, brandId?,
  message: NormalisedMessage,                  // parsed MIME (FR-M1-04)
  conversationId,                              // from threading (FR-M1-05)
  auth: {spf, dkim, dmarc, verdict},           // FR-M1-08
  attachments: [{id, type, scan, extractedText, piiMasked}],   // FR-M1-09
  flags: {autoSubmitted, bounce?, duplicateOf?, precedence}    // FR-M1-06/07/12
}

threadKey(msg) = headerChain(Message-ID, In-Reply-To, References)
             ?? fallback(normalisedSubject + participantsSet + timeWindow)   // FR-M1-05

autoReplyCap(address) : allow ≤3 automated / 24h; block if 2 prior auto-replies  // FR-M1-06
```

## 4. Data

Owns **Mailbox**, **Message** (immutable once stored), **Attachment**; creates **Conversation** (shared
with all modules). See [data-model.md](data-model.md). Invariants: a `Message` is never mutated after
store; `auth` results are frozen with the message; every `Attachment` carries a `scan_result` and
`pii_flags` before any downstream module may read `extracted_text`.

## 5. Behaviour & edge cases

- **Threading precedence:** header chain wins; fallback heuristic only when headers absent, and it
  errs toward *new conversation* to avoid cross-customer mixing (three customers on one booking, FR-M1-14).
- **Loop safety** (RSK-06) is layered: header checks → per-address cap → hold-before-send re-check. The
  cap is deterministic and evaluated at Stage 8 (Gate G13), not by a model.
- **Charset/malformed:** decode best-effort, but a hard parse failure quarantines rather than dropping
  (FR-M1-04) — losing a customer email is worse than a delayed human review.
- **Injection surface:** extracted attachment/body text is tagged as untrusted `data` and never treated
  as instructions ([ADR-0016](../adr/0016-content-is-data-not-instructions.md), MOD-07); passport/card
  numbers are masked in-place before any model sees the text (FR-M1-09, FR-M13-06).
- **Hold delay** (FR-M1-13) doubles as the auto-send recall window (FR-M7-16); a same-thread inbound
  during the hold cancels the pending send.

## 6. Failure & degraded mode

- Provider outage → mailbox `degraded`, backoff retry, alert; **no auto-send** while auth/thread state
  is uncertain. Inbound continues to queue.
- Parse/scan failure → quarantine + alert, replayable after fix (NFR-R-02).
- Send failure → never mark sent; idempotent retry (SR-M1-01); escalate after N attempts.
- History import and reprocessing run in a **send-impossible** mode (NFR-R-04).

## 7. Verification

- **Threading unit assertions:** a reply carrying `In-Reply-To` of message A attaches to A's
  conversation; a message with no headers but identical normalised subject + shared participant within
  the window attaches; a same-subject message from an unrelated participant outside the window creates a
  new conversation.
- **Loop-cap assertion:** feeding 5 vacation-auto-replies from one address yields ≤3 automated
  outbound and a hard block after the 2nd inbound auto-reply.
- **Auth-gate assertion:** DMARC=fail ⇒ verification capped `< weak` ⇒ personal-data auto-send blocked.
- **One runnable self-check** (`checks/m1_threading_and_loops.py`, assert-based, no framework): feed a
  fixture of ~12 raw MIME messages (header-threaded reply, headerless fallback, vacation loop ×5,
  duplicate resend, malformed body, non-UTF-8) and assert: conversation count, loop-cap outbound count,
  quarantine count, and that no message is dropped (input count == stored + quarantined). Fails loudly
  if threading, loop-capping, or lossless-ingest logic breaks.

## 8. Open questions

- **OD-11 (mailbox ownership):** primary inbox vs coexistence (FR-M1-16) changes labelling/state model.
- **OD-10 (send delay):** default hold value (60 s here) vs instant send.
- Fallback-threading window length and subject-normalisation rules need tuning against a real archive
  (Phase 0) — the PRD gives the strategy, not the constants.
