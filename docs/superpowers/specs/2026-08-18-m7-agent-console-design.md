# M7 Agent Console — Design (first UI slice)

- **Date:** 2026-08-18
- **Status:** approved (design), pending implementation plan
- **Governing spec:** [`docs/specs/M7-agent-console.md`](../../specs/M7-agent-console.md)
- **Stack decision:** [ADR-0030](../../adr/0030-technology-stack.md) — React + TypeScript + Vite, `./console/`, keyboard-first, WCAG 2.2 AA
- **Backend:** already implemented (ISSUE-0055/0056/0057) — this slice is UI over existing HTTP APIs, no backend change expected

## 1. Purpose & scope

Build the **agent review console**: the human editor's workspace (M7 §1) — a prioritised queue feeding a
three-pane review view with inline evidence, one-keystroke actions, claim/lock, and SLA visibility. This is
the beachhead UI slice; it is the first of 4–5 UI sub-projects (console, analytics dashboards, knowledge
console, tenancy/admin, crisis workspace), each of which gets its own spec → plan → build cycle.

**In scope (FR ids from M7):**
- FR-M7-01 prioritised queue (scored, SLA/breach badges) · FR-M7-02 claim/lock (idle-release, 409-conflict)
- FR-M7-03 three-pane review (thread · draft · evidence) · FR-M7-04 inline citations (hover-highlight,
  unsupported-sentence warning) · FR-M7-05 one-keystroke actions (approve&send / edit&send / reject /
  escalate / snooze / reassign / mark-spam / request-info) · FR-M7-07 read-only booking panel (degraded →
  "unavailable") · FR-M7-19 autonomy indicator (gate reasons)
- FR-M7-12 SLA timers/breach surfaced in the queue

**Out of scope (this slice — later cycles):** analytics dashboards (M10), knowledge browser/admin (M4/M11),
crisis workspace (M9), case full-text search UI (FR-M7-13/14 — API exists, screen deferred), internal
notes/@mentions UI (FR-M7-09/10 — API exists, screen deferred), machine-translation view (FR-M7-08 — no MT
producer yet), SSO redirect/discovery + JWKS rotation, real-time push (WebSocket/SSE), rich-text editor.

## 2. Confirmed decisions

- **① Dev auth/tenant → dev session bar.** The user picks a tenant id and pastes or generates an HS256
  bearer token; the app stores it in the session context and sends `Authorization: Bearer …` +
  `X-Tenant-ID`. The backend already verifies HS256 (`sso.StaticHMAC` / `OIDCVerifier`, ISSUE-0064) and
  scopes by `X-Tenant-ID`. Full SAML/OIDC redirect is a later cycle. Rationale: a working console today
  without the live-IdP dependency.
- **② Live queue → polling.** Poll `GET /queue` on a short interval (default 5s, configurable) plus manual
  refresh, and revalidate after every mutating action. The backend has no WebSocket/SSE; adding push is its
  own backend slice. Ceiling noted; upgrade path is SSE later.
- **③ Draft editor → plain `<textarea>` + citation overlay.** FR-M7-03 says "rich-text"; functional+clean
  fidelity plus the span-based citation model make plain text the right start. Rich-text is a deferred
  enhancement.

## 3. Architecture

`./console/` — Vite + React + TS, layered so each unit has one purpose and a typed interface:

| Unit | Responsibility | Depends on |
|---|---|---|
| `src/api/client.ts` | One typed fetch wrapper: injects `X-Tenant-ID` + bearer, parses JSON, maps non-2xx → typed errors (`PermissionError` 403, `ClaimConflict` 409, `MissingTenant` 400, `ApiError` other). No React. | session token/tenant |
| `src/api/types.ts` | TS types mirroring the backend JSON (QueueItem, ReviewSurface, Citation, BookingPanel, AutonomyIndicator, ActResult). Hand-written from the Go response shapes. | — |
| `src/session/` | React context holding `{tenantId, token, setSession, clear}`, persisted to `sessionStorage`; the dev session bar sets it. | — |
| `src/queue/` | Queue list screen: polls `GET /queue`, renders scored rows with SLA/breach badges + lock state; claim action (`POST /queue/claim`) with the 409-loser path; row → opens review. | api, session |
| `src/review/` | Three-pane review screen composed of `ThreadPane`, `DraftEditor`, `EvidencePane` (citations + `BookingPanel` + `AutonomyIndicator`), and `ActionBar` (keyboard-first). Loads `GET /queue/review`; actions via `POST /queue/act`. | api, session |
| `src/ui/` | Small shared primitives + design tokens (accessible button, badge, pane, toast, key-hint), restrained utilitarian styling, WCAG-AA contrast + focus rings. | — |
| `src/app/` | Router (queue ⇄ review), top-level layout, session bar, global error/toast boundary. | all |

Kept deliberately small and framework-light: no state-management library (React context + local state +
a tiny polling hook), no component kit (own thin `ui/` primitives), no CSS framework beyond tokens +
CSS modules. YAGNI — add only when a real need appears.

## 4. Backend API consumed (already built)

- `GET /queue` → `{items: QueueItem[]}` (scored; `sla{defined,remainingSecs,breached}`, `locked`,
  `claimedBy`). Tenant via `X-Tenant-ID` (400 if missing).
- `POST /queue/claim` `{conversation_id, agent}` → 200 `{expires_at}` | **409** (already claimed).
- `POST /queue/resolve` `{conversation_id, agent}` → 200.
- `GET /queue/review?conversation_id=…` → three-pane data: customer thread, latest draft + `citations[]`
  (`{claimSpan, knowledgeItemId|bookingFieldPath, score}`), evidence sources, verification-gated booking
  panel, `autonomyIndicator` (gate condition vector + confidence band), translation view (mt_available
  flag).
- `POST /queue/act` `{conversation_id, agent, action, edited_body?}` → `ActResult` (approve_send →
  idempotent Deliver, 409 on double; edit_send → captures ReviewAction; escalate/reject). Guard: nil Sender
  → 503 (fail-closed). G14: server never includes internal notes in the send payload.
- `POST /queue/search`, `/queue/notes`, `/queue/views`, `/queue/escalate` exist but their screens are out
  of scope this slice.

The client is written to these contracts; if a field is missing at runtime the pane shows a gap/unavailable
state, never a crash (mirrors the backend's own "gap indicator, never interpolate" stance).

## 5. Data flow

dev session bar sets `{tenantId, token}` → queue screen polls `GET /queue` (5s) → user claims a row
(`POST /queue/claim`; 409 → toast "claimed by X", refresh) → review screen loads `GET /queue/review` →
panes render (unsupported sentences flagged, booking degraded → "unavailable", autonomy reasons shown) →
keyboard action → `POST /queue/act` → on success toast + return to queue + revalidate.

## 6. Error handling & fallbacks (per M7 spec fallback rows)

- Missing draft → review shows "abstained / escalated" with the reason (not an empty pane).
- Uncited sentence → warning styling (FR-M7-04 fallback).
- Connector degraded / no booking → booking panel "booking data unavailable", agent still triages.
- 403 → "insufficient role" inline; 409 claim → "claimed by X"; 400 missing tenant → session bar prompt;
  Sender unavailable (503) → "sending unavailable, escalate instead".
- Any fetch failure renders inline in the affected pane; the app never shows a blank screen.

## 7. Accessibility (ADR-0030: WCAG 2.2 AA)

Full keyboard operation (FR-M7-05): every action has a key binding shown in the action bar; queue and panes
are tab-navigable with visible focus; AA contrast on tokens; ARIA roles on panes/lists/toasts; the citation
highlight is not colour-only (also underline/marker).

## 8. Testing (test-first, matches repo norm)

- **Vitest + React Testing Library**, against a **mocked `api/client`**: queue ordering + SLA/breach badge,
  claim-conflict (409) path, citation hover→highlight + unsupported marking, each keyboard action → correct
  `act` call, and the error/fallback states (missing draft, degraded booking, 403).
- **One thin smoke E2E** against a running backend (dev token + seeded tenant): login → queue loads → open
  review → an action round-trips. Kept minimal; the backend behaviours are already E2E-covered.
- `console/` gets its own `package.json` scripts (`test`, `build`, `lint`, `typecheck`); CI wiring is a
  follow-up note, not part of this slice.

## 9. Milestones (for the implementation plan)

1. Scaffold `console/` (Vite+React+TS, tokens, router, layout, session context + dev session bar).
2. `api/` typed client + types + error mapping (unit-tested).
3. Queue screen (poll, scored rows, SLA/breach, claim + 409) — tests.
4. Review screen: ThreadPane + DraftEditor + EvidencePane (citations/booking/autonomy) + ActionBar — tests.
5. Wire actions to `/queue/act`, fallbacks, accessibility pass — tests.
6. Smoke E2E against the running backend; README for `console/`.

## 10. Risks / notes

- Backend response field names are read from the Go structs during implementation; `api/types.ts` is the
  single place they're pinned, so a drift shows up in one file.
- Polling interval and token handling are dev-grade by decision ①/②; production auth + push are explicit
  later slices, not silent debt (recorded here and in the console README).
- No backend change is expected. If the UI needs a field the API doesn't expose, that becomes a small
  backend issue (its own ISSUE-00xx), not an in-UI workaround.
