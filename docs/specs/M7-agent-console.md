# M7 — Agent console — Specification

- **PRD module:** §7 M7
- **Depends on:** M2 (booking panel, verification), M3 (classification/overrides), M5 (draft, citations), M6 (autonomy outcome + reasons), M8 (feedback capture), M12 (booking reads)
- **Consumed by:** M8 (edits → learning), M10 (analytics), M13 (audit export)

## 1. Purpose & scope

The workspace for the human editor. Optimised for a fast reviewer with a good draft, not for composing
from scratch (principle 4). Presents a prioritised queue, a three-pane review view with inline evidence,
one-keystroke actions, structured feedback on edit, a read-only booking panel, a translation view, a
searchable/filterable case list, and a complete per-case audit trail. Full keyboard operation — the
mouse is the bottleneck.

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed / notes |
|---|---|---|---|
| FR-M7-01 | Prioritised queue ordered by a configurable score over urgency, departure proximity, SLA remaining, risk class, sentiment, age. | M | Score inputs missing → item still surfaces (never hidden), sorted by age. |
| FR-M7-02 | Claim/lock: opening a case locks it to the agent with a visible lock + idle-release timeout. Two agents never answer the same customer. | M | Lock service down → block second opener, warn. |
| FR-M7-03 | Three-pane review: customer message + thread history · editable rich-text draft · evidence panel (cited sources, booking summary, customer history). | M | Missing draft → show "abstained/escalated" with reason. |
| FR-M7-04 | Inline citations: hovering/selecting a claim highlights its source; unsupported sentences visually marked. | M | Uncited sentence rendered with warning styling. |
| FR-M7-05 | One-keystroke actions: approve&send, edit&send, reject&rewrite, escalate, snooze, reassign, mark-spam, request-info. Full keyboard operation. | M | — |
| FR-M7-06 | Structured feedback on edit: reason code (wrong fact / missing info / wrong tone / wrong language / policy issue / customer-specific / other) + optional comment; one click; skippable (so it isn't gamed). | M | Skipped → edit still captured (M8) without reason. |
| FR-M7-07 | Booking panel: itinerary, flights, hotel, pax, payment status, documents, change/cancel policy, prior contacts — read-only from connector. | M | No connector / degraded → show "booking data unavailable", context-only. |
| FR-M7-08 | Translation view: original · machine translation · draft · back-translation of draft; MT clearly labelled. | M | MT unavailable → show original + draft, note MT missing. |
| FR-M7-09 | Internal notes + @mentions, never customer-visible, with hard visual distinction from outbound text. | M | Internal text must be structurally impossible to send (G14). |
| FR-M7-10 | Escalation to named person/team/queue with a reason, preserving context. | M | — |
| FR-M7-11 | Snippets/canned responses (tenant + personal), shortcut-insertable. | S | — |
| FR-M7-12 | SLA timers per case with breach warnings; SLA config per tenant/intent/channel. | M | Undefined SLA → no timer, not a breach. |
| FR-M7-13 | Case list with saved views/filters (FR-M7-14) + full-text search over bodies, customer, booking ref, destination, draft content. | M | Search index lag → show freshness note. |
| FR-M7-14 | Filter dimensions: status, intent, risk class, autonomy outcome, confidence band, language, brand/mailbox, assigned agent, SLA state, sentiment, destination, departure window, date range, edited-vs-unedited, audit result, tags — combinable, shareable as a saved view. | M | — |
| FR-M7-15 | Case detail / audit trail: full chronological record — inbound, classification, identity decision, retrieved sources, model+prompt versions, draft, gate evaluation (per-condition), human edits with diff, send record, customer reply. Exportable. | M | Any missing stage shown as gap, never fabricated. |
| FR-M7-16 | Undo/recall window for auto-sent messages within the hold delay; documented "cannot recall a delivered email" after. | S | After delivery → clearly state non-recall. |
| FR-M7-17 | Bulk actions on a filtered set: assign, tag, close, apply a reviewed cluster answer (M9). | S | Cluster apply gated by M9 approval. |
| FR-M7-18 | Accessibility: WCAG 2.2 AA. | S | — |
| FR-M7-19 | Indicator of current autonomy level and whether this case was auto-send-eligible **and why not** (from GateResult.reasonsForAgent). | S | Transparency reduces "the robot is doing something weird" anxiety. |

**SR-M7-01** *(addition)* — Send is a two-source guard: the console MUST re-check the current kill-switch
/ thread-takeover / lock state at the moment of send (not only at open), so a supervisor kill (FR-M6-04)
or a concurrent human reply (G12) between open and send prevents a stale send.

## 3. Interfaces

```
getQueue(agent, filters) -> QueueItem[]         // scored, FR-M7-01
claimCase(caseId, agent) -> Lock | Conflict     // FR-M7-02
getReviewView(caseId) -> { message, thread, draft, citations, evidence, booking, gateResult }
act(caseId, action, payload) -> Result          // approve/edit/reject/escalate/snooze/... FR-M7-05
captureFeedback(caseId, reasonCode?, comment?) -> void   // FR-M7-06 -> M8
searchCases(tenant, query, filters, savedView?) -> Page  // FR-M7-13/14
getAuditTrail(caseId) -> AuditRecord[]          // FR-M7-15, exportable
```

Every `act` writes a **ReviewAction** (diff, edit-distance, reason code) → M8, and an **AuditRecord** → M13.

## 4. Data

Reads Draft/Citation/GateEvaluation/Booking(cached); writes ReviewAction and SentMessage
([data-model](data-model.md)). Internal notes stored on the conversation with a hard `internal` flag that
the send path refuses to serialise (SR-M7-01, G14).

## 5. Behaviour & edge cases

- **Locking** prevents the classic double-reply; idle timeout releases stale locks (FR-M7-02).
- **Edit-distance** is computed on save-and-send between the presented draft and the sent text
  (FR-M8-01) — the honest quality metric (O4).
- **Translation view** lets a Romanian team supervise Danish/Polish replies (O5, RSK-14); MT always
  labelled so a reviewer knows what is machine-produced.
- **Autonomy transparency** (FR-M7-19) surfaces the exact failing gate conditions from M6.

## 6. Failure & degraded mode

Connector down → booking panel shows unavailable, agent still triages/answers content (principle 7).
Search lag → results annotated. Lock service down → fail *closed* (block the second opener) to preserve
the no-double-reply invariant.

## 7. Verification

- **One runnable check** (`internal_note_never_sends`, assert-based): construct a conversation with an
  internal note + @mention and an outbound draft; assert the serialised outbound payload contains none of
  the internal-note text and none of the @mention (G14 guarantee). A second assertion: `claimCase` twice
  concurrently yields exactly one `Lock` and one `Conflict` (FR-M7-02 no-double-reply).
- Accessibility checked with an automated WCAG 2.2 AA pass in CI (FR-M7-18).

## 8. Open questions

- OD-11 mailbox ownership / coexistence affects whether the console is primary inbox
  ([ADR-0026](../adr/0026-coexistence-capable-mailbox-ownership.md)).
- OD-04 standalone vs helpdesk add-on determines whether this console is the core asset or a thin layer
  ([ADR-0020](../adr/0020-standalone-core-optional-helpdesk-interop.md)).
