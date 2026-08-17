# M9 — Crisis / mass-event mode — Specification

- **PRD module:** §7 M9 (also opportunity O1)
- **Depends on:** M3 (intent/entity/topic), M4 (knowledge + automation freeze), M5 (personalised generation), M6 (autonomy gate, per-topic freeze), M2 (booking attributes), M12 (affected-booking lookup)
- **Consumed by:** M7 console (Event workspace, bulk actions), M10 analytics (event reporting)

## 1. Purpose & scope

Crisis mode turns a spike of related inbound email (airline collapse, wildfire, strike, mass flight
change) into a single controlled response operation. It detects the anomaly, clusters the surge by
topic and affected booking attributes, gives a supervisor one place to author the operator's official
position, personalises that one answer across every affected case, and releases the drafts for bulk
approval or policy-permitting auto-send. Its safety spine is the **automation freeze** (FR-M9-05): while
a disruption is live the knowledge base is by definition stale, so the module suppresses ordinary
autonomy on the affected topic until an authored position exists. Boundary: M9 detects and orchestrates;
it does not itself change bookings (write-back is out of scope, [ADR-0009](../adr/0009-reservation-connector-interface.md)).

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M9-01 | Detect volume anomalies overall and per topic/destination against a rolling seasonal baseline; alert supervisors within minutes of the threshold breach. | M | If baseline data is missing, use an absolute-rate fallback threshold and still alert; never suppress an alert on missing history. |
| FR-M9-02 | Cluster the surge by semantic topic (M3) and affected booking attributes (destination, departure date, flight, hotel, supplier). Each case belongs to at most one active cluster. | M | Uncertain cluster membership → leave the case in the normal queue, never force it into a cluster answer. |
| FR-M9-03 | Provide an **Event workspace**: create an Event, attach clusters and affected bookings, and record the operator's **official position** as a single versioned authored statement. | M | No Event may leave "position empty" and still release drafts (see FR-M9-05). |
| FR-M9-04 | **Cluster answer**: a supervisor writes/approves one answer; system personalises it per customer using booking data + language (M5), produces per-case drafts, and releases them for bulk approval or (policy permitting) auto-send. | M | Any per-case draft failing its own gate (M6) or commitment guardrail drops out of the bulk set to individual human review; it is never sent as part of a bulk release. |
| FR-M9-05 | **Automation freeze** on the affected topic until the official position exists. Safety default, not an option. | M | Freeze is the default state the instant an Event is opened; lifting requires an authored, versioned position + explicit supervisor action. |
| FR-M9-06 | **Proactive outbound** to affected customers who have not written in, subject to explicit supervisor approval per send batch. | S | No proactive send without per-batch supervisor approval and a suppression check against exclusion lists / already-contacted. |
| FR-M9-07 | Event reporting: customers affected, contacted, resolved, still open — exportable. | S | Report renders from logged facts only; if a figure is uncomputable it shows "unknown", never a guess. |

**Spec additions**

- **SR-M9-01** *(addition)*: An Event has an explicit lifecycle `detected → triaged → position_drafting → active → winding_down → closed`; the automation freeze is bound to `active`-and-earlier states, auto-lifted only at supervisor action.
- **SR-M9-02** *(addition)*: Cluster reassignment (a case moved between clusters) is audited and re-runs personalisation for that case only.

## 3. Interfaces

```
// Detection (runs continuously per tenant)
event AnomalyDetected { tenant_id, scope: {overall | topic | destination}, key, observed_rate,
                        baseline_rate, z_score, window, sample_case_ids[] }

// Orchestration API (supervisor-driven, RBAC: Supervisor)
createEvent(tenant_id, title, seed_cluster_ids[]) -> event_id
attachClusters(event_id, cluster_ids[])
setOfficialPosition(event_id, text, source_refs[]) -> position_version   // increments version
listAffectedBookings(event_id) -> booking_ref[]                          // via M12 attribute match
generateClusterDrafts(event_id, position_version) -> {draft_id, case_id, gate_outcome}[]
releaseClusterDrafts(event_id, mode: bulk_review | auto_if_allowed)
approveProactiveBatch(event_id, recipient_set, position_version) -> batch_id   // FR-M9-06

// Freeze control (consumed by M6 gate)
isTopicFrozen(tenant_id, topic_key) -> bool          // gate calls this; frozen ⇒ force human
```

Consumes: `M3.classify` (topic), `M4.retrieve` (down-weighted during freeze), `M5.personalise`,
`M12.findBookingsBy*` and attribute queries, `M6.evaluateGate` (per personalised draft),
`M2` verification level (personal facts in a cluster answer still require it).

## 4. Data

Owns **Event** (§10): `tenant, title, official_position (versioned), clusters, affected_bookings,
status`. Uses **Conversation** (cluster membership tag), **Draft** (per-case personalised), **Customer**
(exclusion/consent for proactive). Invariants: an Event's official position is append-only versioned;
a Conversation references at most one active Event; personalised drafts record which `position_version`
produced them so a position edit invalidates stale drafts.

## 5. Behaviour & edge cases

- **Detection.** Per-topic and per-destination counters over a rolling window compared to a seasonal
  baseline (same calendar period, prior seasons where available). Alert on z-score/ratio breach; the
  overall-volume detector catches novel topics the topic detector has no baseline for.
- **Freeze precedence.** The freeze (FR-M9-05) overrides any per-intent auto-send allowlist: an R0
  content intent normally auto-sendable at L2 is forced to human while its topic is frozen, because the
  underlying facts (e.g. "your flight departs 06:15") may now be false.
- **Personalisation still respects identity.** A cluster answer that includes personal facts is gated
  per case on verification level (M2) and the commitment guardrail (M5/FR-M5-06) — a bulk operation does
  not bypass either. Cases that cannot be verified fall to individual human handling.
- **Position edit mid-flight.** Editing the official position bumps its version and marks all drafts
  built from the prior version stale; released-but-unsent drafts in the hold window ([ADR-0021](../adr/0021-configurable-hold-before-send-default-60s.md)) are recalled and regenerated.
- **Proactive dedup.** Proactive outbound must suppress recipients who already wrote in (they get the
  reactive answer) and anyone on an exclusion list (FR-M6-08).
- **Winding down.** When inbound on the topic falls below baseline the Event auto-suggests
  `winding_down`; freeze is only lifted by explicit supervisor action, never automatically.

## 6. Failure & degraded mode

- Detector outage → no clustering, but ordinary per-case pipeline still runs (degraded, not down);
  raise an ops alert (NFR-R-03) because a missed crisis is a reputational event.
- No booking connector (degraded mode, FR-M12-04) → cluster by topic only; affected-booking figures
  show "unknown"; proactive outbound disabled (cannot know who is affected).
- Personalisation model outage → drafts fall to the plain official position for human review; never
  auto-sent.

## 7. Verification

- **Self-check (runnable):** `check_m9_freeze.py` — construct an Event in `active` state with an empty
  position; assert `isTopicFrozen(tenant, topic) == True` and that `evaluateGate()` for an otherwise
  L2-eligible R0 case in that topic returns `human_review`. Then set a position + supervisor lift and
  assert the gate returns to normal. One file, asserts only, no framework.
- Unit: clustering assigns each seed case to exactly one cluster; position edit invalidates prior-version
  drafts; proactive batch excludes already-contacted + exclusion-listed recipients.
- Eval-set hook: a synthetic "flight cancelled" surge replayed in sandbox (FR-M11-07) must (a) trip the
  anomaly detector, (b) freeze the topic, (c) produce personalised drafts none of which contain a
  fabricated commitment.

## 8. Open questions

- Anomaly thresholds per tenant vs global defaults — needs pilot data (relates to Phase 0, §15).
- Proactive outbound consent basis in some markets may be constrained; confirm with M13/legal before
  enabling FR-M9-06 per tenant.
