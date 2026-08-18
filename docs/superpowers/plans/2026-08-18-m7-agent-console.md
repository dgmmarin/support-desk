# M7 Agent Console Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the agent review console — a prioritised queue feeding a three-pane review view with inline citations, one-keystroke actions, and claim/lock — as a React+TS+Vite app over the existing `/queue/*` backend APIs.

**Architecture:** A small, framework-light SPA in `./console/`. A typed `api/` client wraps fetch (injects `X-Tenant-ID` + bearer, maps status codes to typed errors); a `session/` React context holds tenant+token set by a dev session bar; `queue/` polls and claims; `review/` renders the three panes + keyboard action bar. No state library, no component kit, no CSS framework — React context + local state + a polling hook + a thin `ui/` token layer.

**Tech Stack:** React 18, TypeScript 5, Vite 5, Vitest + @testing-library/react + jsdom, CSS modules + CSS custom-property tokens.

**Spec:** `docs/superpowers/specs/2026-08-18-m7-agent-console-design.md`

## Global Constraints

- **Location:** all code under `./console/`. Do not modify `./backend`.
- **Stack floors (ADR-0030):** React + TypeScript + Vite. WCAG 2.2 AA: full keyboard operation, visible focus, AA contrast, citation highlight never colour-only.
- **Auth/tenant:** every API call sends `X-Tenant-ID: <tenantId>` + `Authorization: Bearer <token>` from the session context (decision ①).
- **Live updates:** polling only (decision ②), default 5000 ms, revalidate after each mutation.
- **Draft editor:** plain `<textarea>` (decision ③). No rich-text library.
- **Test-first:** every logic unit gets a failing Vitest test first. Commit after each green task. No co-authored/signed-off trailers. Run `npm test`, `npm run typecheck`, `npm run lint` green before every commit.
- **WIRE TYPES ARE SNAKE_CASE AND FIXED BY TASK 3.** `src/api/types.ts` already exists and mirrors the real Go `json:` tags. Response objects are snake_case (`conversation_id`, `risk_class` [a **number**], `inline_citations`, `draft_available`, `evidence`, `reasons_for_agent`, …). Only the client-side `ActRequest` DTO is camelCase; `client.ts` maps it to the wire. Every screen below reads snake_case fields — do not reintroduce camelCase field access. The authoritative shapes are in `console/src/api/types.ts`; read it before writing any screen.

---

### Task 1: Scaffold the console app

**(COMPLETE — commit range 166b3c3..d621ec3.)** Vite+React+TS app under `console/`: `package.json` (scripts dev/build/typecheck/test/lint), tsconfig, `vite.config.ts` (imports `defineConfig` from `vitest/config`, jsdom test env), `vitest.setup.ts`, `index.html`, `src/main.tsx`, `src/app/App.tsx` (`<h1>TourDesk Console</h1>`), `src/ui/tokens.css`, `eslint.config.js` (typescript-eslint flat config), `.gitignore`, `README.md`. Left here for the record; do not re-run.

---

### Task 2: Session context + dev session bar

**(COMPLETE — commit b8de04d.)** `src/session/SessionContext.tsx` exports `type Session = { tenantId: string; token: string }`, `useSession()` → `{ session, setSession, clear }`, `<SessionProvider>` (sessionStorage key `td.session`); `src/session/SessionBar.tsx`. Do not re-run.

---

### Task 3: Typed API client + response types

**(COMPLETE — commit 1d46f76.)** `src/api/types.ts` + `src/api/client.ts` + `client.test.ts`. Types reconciled to the real backend wire (snake_case, `review.Surface` shape, 4 actions). Exports: types `Sla, QueueItem, Message, Citation, EvidenceSource, BookingPanel, GateCondition, AutonomyIndicator, TranslationView, ReviewSurface, ActionKind, ActRequest, ActResult`; `ApiError/PermissionError/ClaimConflict/MissingTenant`; `makeClient(getSession, fetchImpl?)` → `{ getQueue, claim, resolve, getReview, act }`; `type ApiClient`. Do not re-run. **Tasks 4–7 below were rewritten against these real types.**

Reference shapes (from `types.ts` — the source of truth):
- `QueueItem`: `conversation_id, score, risk_class (number), intent?, channel?, enqueued_at, departure_at?, sla, status, queue, locked, claimed_by?`; `Sla`: `defined, target_at?, remaining_secs, window_secs?, breached`.
- `ReviewSurface`: `conversation_id, customer_message: Message, thread: Message[], draft_available, draft, draft_language?, draft_status?, inline_citations: Citation[], unsupported_claims?: string[], evidence: EvidenceSource[], booking: BookingPanel, autonomy: AutonomyIndicator, translation`.
- `Message`: `from, direction, subject?, body, automated` (NO timestamp).
- `Citation`: `claim_span, knowledge_item_id?, booking_field_path?, score?, resolved`.
- `EvidenceSource`: `id, title?, url?, score?` (NO snippet).
- `BookingPanel`: `available, withheld, reason?, ref?, status?, destination?, dates?: string[], payment_status?, balance_due?, accommodation?, transport?`.
- `AutonomyIndicator`: `present, outcome?, route?, auto_send_eligible, confidence_band?, conditions?: GateCondition[], reasons_for_agent?: string[]` (NO `level`). `GateCondition`: `ID, Pass, Detail`.
- `ActionKind`: `"approve_send" | "edit_send" | "reject" | "escalate"` (only these 4).
- `ActRequest` (camelCase DTO): `conversationId, agent, action, editedBody?, targetQueue?, reason?, reasonCode?, comment?`. `ActResult`: `action, sent?, already_sent?, sent_message_id?, escalated?, rejected?`.

---

### Task 4: Queue screen (poll, scored rows, SLA/breach, claim + 409)

**Files:**
- Create: `console/src/queue/usePolling.ts`, `console/src/queue/QueueScreen.tsx`
- Test: `console/src/queue/QueueScreen.test.tsx`

**Interfaces:**
- Consumes: `ApiClient` (Task 3), `QueueItem` (snake_case).
- Produces: `<QueueScreen client={ApiClient} onOpen={(id: string) => void} />`; `usePolling(fn, ms)`.

- [ ] **Step 1: Write the failing test** (`QueueScreen.test.tsx`)

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueueScreen } from "./QueueScreen";
import { ClaimConflict } from "../api/client";
import type { QueueItem } from "../api/types";
const items: QueueItem[] = [
  { conversation_id: "c1", score: 9, risk_class: 1, intent: "faq", channel: "email", enqueued_at: "", sla: { defined: true, remaining_secs: -60, window_secs: 3600, breached: true }, locked: false, status: "pending", queue: "normal" },
  { conversation_id: "c2", score: 3, risk_class: 0, intent: "faq", channel: "email", enqueued_at: "", sla: { defined: true, remaining_secs: 1800, window_secs: 3600, breached: false }, locked: false, status: "pending", queue: "normal" },
];
function client(overrides = {}) {
  return { getQueue: async () => items, claim: async () => ({ expires_at: "" }), resolve: async () => ({ resolved: true }), getReview: async () => ({} as any), act: async () => ({} as any), ...overrides } as any;
}
test("renders rows ordered by score with a breach badge", async () => {
  render(<QueueScreen client={client()} onOpen={() => {}} />);
  const rows = await screen.findAllByRole("row");
  expect(within(rows[1]).getByText("c1")).toBeInTheDocument();
  expect(within(rows[1]).getByText(/breach/i)).toBeInTheDocument();
});
test("claim conflict shows a message and does not open", async () => {
  const onOpen = vi.fn();
  const c = client({ claim: async () => { throw new ClaimConflict(409, "x"); } });
  render(<QueueScreen client={c} onOpen={onOpen} />);
  await userEvent.click(await screen.findByRole("button", { name: /claim c1/i }));
  expect(await screen.findByText(/already claimed/i)).toBeInTheDocument();
  expect(onOpen).not.toHaveBeenCalled();
});
```

- [ ] **Step 2: Run test to verify it fails** — `cd console && npm test -- QueueScreen` → FAIL (cannot resolve `./QueueScreen`).

- [ ] **Step 3: Implement `usePolling.ts`**

```ts
import { useEffect, useRef, useState } from "react";
export function usePolling<T>(fn: () => Promise<T>, ms: number): { data: T | null; error: unknown; refresh: () => void } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<unknown>(null);
  const fnRef = useRef(fn); fnRef.current = fn;
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    const run = () => fnRef.current().then((d) => alive && setData(d)).catch((e) => alive && setError(e));
    run();
    const id = setInterval(run, ms);
    return () => { alive = false; clearInterval(id); };
  }, [ms, tick]);
  return { data, error, refresh: () => setTick((t) => t + 1) };
}
```

- [ ] **Step 4: Implement `QueueScreen.tsx`** (snake_case fields; `risk_class` is a number rendered `R{n}`)

```tsx
import { useState } from "react";
import type { ApiClient } from "../api/client";
import { ClaimConflict } from "../api/client";
import { usePolling } from "./usePolling";
export function QueueScreen({ client, onOpen }: { client: ApiClient; onOpen: (id: string) => void }) {
  const { data, refresh } = usePolling(() => client.getQueue(), 5000);
  const [msg, setMsg] = useState<string | null>(null);
  const rows = (data ?? []).slice().sort((a, b) => b.score - a.score);
  async function claim(id: string) {
    setMsg(null);
    try { await client.claim(id); onOpen(id); refresh(); }
    catch (e) { setMsg(e instanceof ClaimConflict ? "already claimed by another agent" : "claim failed"); }
  }
  return (
    <section aria-label="case queue">
      {msg && <p role="alert" style={{ color: "var(--danger)" }}>{msg}</p>}
      <table><thead><tr><th>Case</th><th>Score</th><th>Risk</th><th>Intent</th><th>SLA</th><th></th></tr></thead>
        <tbody>
          {rows.map((it) => (
            <tr key={it.conversation_id}>
              <td>{it.conversation_id}</td><td>{it.score.toFixed(1)}</td><td>{`R${it.risk_class}`}</td><td>{it.intent ?? "—"}</td>
              <td>{it.sla.breached
                ? <span style={{ color: "var(--danger)", fontWeight: 600 }}>⚠ breach</span>
                : it.sla.defined ? `${Math.round(it.sla.remaining_secs / 60)}m` : "—"}</td>
              <td><button onClick={() => claim(it.conversation_id)} aria-label={`claim ${it.conversation_id}`}>Claim</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
```

- [ ] **Step 5: Run tests** — `cd console && npm test -- QueueScreen` → PASS (2).
- [ ] **Step 6: Gates + commit** — `npm test && npm run typecheck && npm run lint` green, then:

```bash
git add console/src/queue && git commit -m "feat(console): queue screen — polling, scored rows, SLA breach, claim 409"
```

---

### Task 5: Review screen — thread + evidence (citations, booking, autonomy)

**Files:**
- Create: `console/src/review/ThreadPane.tsx`, `console/src/review/EvidencePane.tsx`, `console/src/review/ReviewScreen.tsx`
- Test: `console/src/review/EvidencePane.test.tsx`, `console/src/review/ReviewScreen.test.tsx`

**Interfaces:**
- Consumes: `ApiClient.getReview`, `ReviewSurface`/`Message`/`BookingPanel`/`AutonomyIndicator` (snake_case).
- Produces: `<ReviewScreen client conversationId agent onDone />`; `<EvidencePane surface={ReviewSurface} />`; `<ThreadPane customerMessage={Message} thread={Message[]} />`.

Notes on the real shape (gaps handled, not faked): messages have **no timestamp** (show `from` + `direction`); evidence sources have **no snippet** (show `title`/`url`); autonomy has **no `level`** (show `outcome`/`route`); the draft is `draft_available` (bool) + `draft` (string) + `draft_status` (the abstain reason when unavailable).

- [ ] **Step 1: Write the failing test** (`EvidencePane.test.tsx`)

```tsx
import { render, screen } from "@testing-library/react";
import { EvidencePane } from "./EvidencePane";
import type { ReviewSurface } from "../api/types";
const surface: ReviewSurface = {
  conversation_id: "c1", customer_message: { from: "cust@x", direction: "inbound", body: "When do I depart?", automated: false }, thread: [],
  draft_available: true, draft: "You depart 09:00.", inline_citations: [{ claim_span: "You depart 09:00.", booking_field_path: "itinerary.departure", resolved: true }],
  evidence: [{ id: "k1", title: "FAQ" }],
  booking: { available: false, withheld: false, reason: "connector down" },
  autonomy: { present: true, outcome: "human_review", route: "review", auto_send_eligible: false, reasons_for_agent: ["G05 confidence below threshold"] },
  translation: { original_message: "", draft: "", mt_available: false },
};
test("shows degraded booking and the autonomy reasons", () => {
  render(<EvidencePane surface={surface} />);
  expect(screen.getByText(/booking data unavailable/i)).toBeInTheDocument();
  expect(screen.getByText(/G05 confidence below threshold/)).toBeInTheDocument();
});
test("renders a source list", () => {
  render(<EvidencePane surface={surface} />);
  expect(screen.getByText("FAQ")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails** — `npm test -- EvidencePane` → FAIL.

- [ ] **Step 3: Implement `ThreadPane.tsx` and `EvidencePane.tsx`**

`ThreadPane.tsx`:
```tsx
import type { Message } from "../api/types";
function Msg({ m }: { m: Message }) {
  return (
    <article style={{ borderBottom: "1px solid var(--line)", padding: 8 }}>
      <div style={{ color: "var(--muted)", fontSize: 12 }}>{m.from} · {m.direction}{m.automated ? " · auto" : ""}</div>
      {m.subject && <div style={{ fontWeight: 600 }}>{m.subject}</div>}
      <div>{m.body}</div>
    </article>
  );
}
export function ThreadPane({ customerMessage, thread }: { customerMessage: Message; thread: Message[] }) {
  return (
    <div aria-label="customer thread" style={{ overflow: "auto" }}>
      <Msg m={customerMessage} />
      {thread.map((m, i) => <Msg key={i} m={m} />)}
    </div>
  );
}
```
`EvidencePane.tsx`:
```tsx
import type { ReviewSurface } from "../api/types";
export function EvidencePane({ surface }: { surface: ReviewSurface }) {
  const { booking, autonomy, evidence } = surface;
  return (
    <aside aria-label="evidence" style={{ overflow: "auto", display: "grid", gap: "var(--pane-gap)" }}>
      <section aria-label="autonomy">
        <h3>Autonomy{autonomy.outcome ? ` — ${autonomy.outcome}` : ""}{autonomy.confidence_band ? ` · ${autonomy.confidence_band}` : ""}</h3>
        {autonomy.auto_send_eligible
          ? <p>Auto-send eligible.</p>
          : <ul>{(autonomy.reasons_for_agent ?? []).map((r, i) => <li key={i}>{r}</li>)}</ul>}
      </section>
      <section aria-label="booking">
        <h3>Booking</h3>
        {booking.available
          ? <dl>{([["ref", booking.ref], ["status", booking.status], ["destination", booking.destination], ["dates", booking.dates?.join(" – ")], ["payment", booking.payment_status], ["balance", booking.balance_due], ["accommodation", booking.accommodation], ["transport", booking.transport]] as const)
              .filter(([, v]) => v).map(([k, v]) => <div key={k}><dt style={{ color: "var(--muted)" }}>{k}</dt><dd>{v}</dd></div>)}</dl>
          : <p style={{ color: "var(--muted)" }}>booking data unavailable{booking.reason ? ` (${booking.reason})` : ""}</p>}
        {booking.available && booking.withheld && <p style={{ color: "var(--warn)" }}>Some details withheld pending verification.</p>}
      </section>
      <section aria-label="sources">
        <h3>Cited sources</h3>
        {evidence.length === 0 ? <p style={{ color: "var(--muted)" }}>none</p>
          : <ul>{evidence.map((s) => <li key={s.id}>{s.title ?? s.id}{s.url ? ` — ${s.url}` : ""}</li>)}</ul>}
      </section>
    </aside>
  );
}
```

- [ ] **Step 4: Run test** — `npm test -- EvidencePane` → PASS (2).

- [ ] **Step 5: Write the failing test for ReviewScreen (abstain fallback)** (`ReviewScreen.test.tsx`)

```tsx
import { render, screen } from "@testing-library/react";
import { ReviewScreen } from "./ReviewScreen";
import type { ReviewSurface } from "../api/types";
function client(surface: ReviewSurface) { return { getReview: async () => surface, act: async () => ({ action: "approve_send" }) } as any; }
test("shows the draft_status when there is no draft", async () => {
  const s: ReviewSurface = { conversation_id: "c1", customer_message: { from: "c", direction: "inbound", body: "hi", automated: false }, thread: [],
    draft_available: false, draft: "", draft_status: "abstained_or_escalated",
    inline_citations: [], evidence: [], booking: { available: false, withheld: false }, autonomy: { present: false, auto_send_eligible: false }, translation: { original_message: "", draft: "", mt_available: false } };
  render(<ReviewScreen client={client(s)} conversationId="c1" agent="me" onDone={() => {}} />);
  expect(await screen.findByText(/abstained_or_escalated/)).toBeInTheDocument();
});
```

- [ ] **Step 6: Implement `ReviewScreen.tsx`** (draft region placeholder; DraftEditor+ActionBar mounted in Task 6)

```tsx
import { useEffect, useState } from "react";
import type { ApiClient } from "../api/client";
import type { ReviewSurface } from "../api/types";
import { ThreadPane } from "./ThreadPane";
import { EvidencePane } from "./EvidencePane";
export function ReviewScreen({ client, conversationId, agent, onDone }: { client: ApiClient; conversationId: string; agent: string; onDone: () => void }) {
  const [surface, setSurface] = useState<ReviewSurface | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { client.getReview(conversationId).then(setSurface).catch(() => setError("could not load case")); }, [client, conversationId]);
  if (error) return <p role="alert">{error}</p>;
  if (!surface) return <p>Loading…</p>;
  return (
    <div style={{ display: "grid", gridTemplateColumns: "1fr 1.2fr 1fr", gap: "var(--pane-gap)", height: "100%" }}>
      <ThreadPane customerMessage={surface.customer_message} thread={surface.thread} />
      <div aria-label="draft">
        {!surface.draft_available
          ? <p role="status" style={{ color: "var(--warn)" }}>{surface.draft_status ?? "abstained / escalated"}</p>
          : <div data-testid="draft-region">{surface.draft}</div>}
      </div>
      <EvidencePane surface={surface} />
      <button onClick={onDone} style={{ position: "absolute", right: 8, top: 8 }}>Back to queue</button>
    </div>
  );
}
```

- [ ] **Step 7: Run tests** — `npm test -- review` → PASS.
- [ ] **Step 8: Gates + commit**

```bash
git add console/src/review && git commit -m "feat(console): review thread + evidence panes (citations, booking, autonomy, abstain fallback)"
```

---

### Task 6: Draft editor + keyboard action bar → /queue/act

**Files:**
- Create: `console/src/review/DraftEditor.tsx`, `console/src/review/ActionBar.tsx`
- Modify: `console/src/review/ReviewScreen.tsx` (mount DraftEditor + ActionBar in the draft region)
- Test: `console/src/review/ActionBar.test.tsx`, `console/src/review/DraftEditor.test.tsx`

**Interfaces:**
- Consumes: `ApiClient.act`, `ReviewSurface`, `Citation`, `ActionKind` (the 4 real actions).
- Produces: `<DraftEditor value onChange unsupported={string[]} />`; `<ActionBar onAct={(a: ActionKind) => void} busy />` with keys a=approve_send, e=edit_send, x=escalate, r=reject.

- [ ] **Step 1: Write the failing test for ActionBar** (`ActionBar.test.tsx`)

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ActionBar } from "./ActionBar";
test("pressing 'a' fires approve_send", async () => {
  const onAct = vi.fn();
  render(<ActionBar onAct={onAct} busy={false} />);
  await userEvent.keyboard("a");
  expect(onAct).toHaveBeenCalledWith("approve_send");
});
test("clicking Escalate fires escalate", async () => {
  const onAct = vi.fn();
  render(<ActionBar onAct={onAct} busy={false} />);
  await userEvent.click(screen.getByRole("button", { name: /escalate/i }));
  expect(onAct).toHaveBeenCalledWith("escalate");
});
```

- [ ] **Step 2: Run test to verify it fails** — `npm test -- ActionBar` → FAIL.

- [ ] **Step 3: Implement `ActionBar.tsx` and `DraftEditor.tsx`**

`ActionBar.tsx` (only the 4 real actions):
```tsx
import { useEffect } from "react";
import type { ActionKind } from "../api/types";
const KEYS: Record<string, ActionKind> = { a: "approve_send", e: "edit_send", x: "escalate", r: "reject" };
const LABEL: Record<ActionKind, string> = { approve_send: "Approve & send (a)", edit_send: "Edit & send (e)", escalate: "Escalate (x)", reject: "Reject (r)" };
export function ActionBar({ onAct, busy }: { onAct: (a: ActionKind) => void; busy: boolean }) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "TEXTAREA" || tag === "INPUT") return;
      const a = KEYS[e.key.toLowerCase()];
      if (a && !busy) { e.preventDefault(); onAct(a); }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onAct, busy]);
  return (
    <div role="toolbar" aria-label="case actions" style={{ display: "flex", gap: 8, borderTop: "1px solid var(--line)", padding: 8 }}>
      {(["approve_send", "edit_send", "escalate", "reject"] as ActionKind[]).map((a) => (
        <button key={a} disabled={busy} onClick={() => onAct(a)}>{LABEL[a]}</button>
      ))}
    </div>
  );
}
```
`DraftEditor.tsx` (uses the wire's `unsupported_claims`):
```tsx
export function DraftEditor({ value, onChange, unsupported }: { value: string; onChange: (v: string) => void; unsupported: string[] }) {
  return (
    <div style={{ display: "grid", gap: 4 }}>
      {unsupported.length > 0 && <p role="status" style={{ color: "var(--warn)" }}>⚠ {unsupported.length} unsupported claim(s): {unsupported.join("; ")}</p>}
      <textarea aria-label="draft reply" value={value} onChange={(e) => onChange(e.target.value)} rows={16} style={{ width: "100%", fontFamily: "inherit" }} />
    </div>
  );
}
```

- [ ] **Step 4: Write the DraftEditor test** (`DraftEditor.test.tsx`)

```tsx
import { render, screen } from "@testing-library/react";
import { DraftEditor } from "./DraftEditor";
test("warns when there are unsupported claims", () => {
  render(<DraftEditor value="Hello." onChange={() => {}} unsupported={["Hello."]} />);
  expect(screen.getByText(/unsupported claim/i)).toBeInTheDocument();
});
```

- [ ] **Step 5: Wire them into `ReviewScreen.tsx`** — replace the `data-testid="draft-region"` block with `<DraftRegion client={client} surface={surface} agent={agent} onDone={onDone} />`, add imports (`useState`, `DraftEditor`, `ActionBar`, `ActionKind`), and append this component:

```tsx
function DraftRegion({ client, surface, agent, onDone }: { client: ApiClient; surface: ReviewSurface; agent: string; onDone: () => void }) {
  const [body, setBody] = useState(surface.draft ?? "");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  async function act(action: ActionKind) {
    setBusy(true); setMsg(null);
    try {
      const r = await client.act({
        conversationId: surface.conversation_id, agent, action,
        editedBody: action === "edit_send" ? body : undefined,
        targetQueue: action === "escalate" ? "specialist" : undefined,   // backend 400s escalate without target_queue
        reason: action === "reject" ? "rejected by agent" : action === "escalate" ? "escalated by agent" : undefined,
      });
      setMsg(r.sent || r.already_sent ? "sent" : r.escalated ? "escalated" : r.rejected ? "rejected" : "done");
      onDone();
    } catch (e) {
      setMsg(e instanceof Error && e.message.includes("503") ? "sending unavailable — escalate instead" : "action failed");
    } finally { setBusy(false); }
  }
  return (<div>
    {msg && <p role="status">{msg}</p>}
    <DraftEditor value={body} onChange={setBody} unsupported={surface.unsupported_claims ?? []} />
    <ActionBar onAct={act} busy={busy} />
  </div>);
}
```

- [ ] **Step 6: Run tests** — `npm test -- review` → PASS.
- [ ] **Step 7: Gates + commit**

```bash
git add console/src/review && git commit -m "feat(console): draft editor + keyboard action bar wired to /queue/act"
```

---

### Task 7: App router, session gate, accessibility pass, smoke test

**Files:**
- Modify: `console/src/app/App.tsx` (router: session gate → queue ⇄ review, mount SessionBar), `console/src/app/App.test.tsx` (wrap already handled by App internally)
- Create: `console/src/app/App.smoke.test.tsx`

**Interfaces:** consumes everything above; produces the complete app: no session → prompt; session → QueueScreen; open → ReviewScreen; back → QueueScreen.

- [ ] **Step 1: Write the failing smoke test** (`App.smoke.test.tsx`, snake_case surface fixture)

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "./App";
import type { ReviewSurface } from "../api/types";
test("with a session, shows the queue and opens a case", async () => {
  const surface: ReviewSurface = { conversation_id: "c1", customer_message: { from: "c", direction: "inbound", body: "hi", automated: false }, thread: [],
    draft_available: true, draft: "Hi there.", inline_citations: [], evidence: [], booking: { available: false, withheld: false },
    autonomy: { present: true, auto_send_eligible: false }, translation: { original_message: "", draft: "", mt_available: false } };
  const client = {
    getQueue: async () => [{ conversation_id: "c1", score: 5, risk_class: 0, enqueued_at: "", sla: { defined: false, remaining_secs: 0, breached: false }, locked: false, status: "pending", queue: "normal" }],
    claim: async () => ({ expires_at: "" }), resolve: async () => ({ resolved: true }),
    getReview: async () => surface, act: async () => ({ action: "approve_send", sent: true }),
  } as any;
  sessionStorage.setItem("td.session", JSON.stringify({ tenantId: "t1", token: "jwt" }));
  render(<App client={client} />);
  await userEvent.click(await screen.findByRole("button", { name: /claim c1/i }));
  expect(await screen.findByLabelText("draft reply")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails** — `npm test -- App.smoke` → FAIL (App takes no `client`, no routing).

- [ ] **Step 3: Implement routing in `App.tsx`**

```tsx
import "../ui/tokens.css";
import { useState } from "react";
import { SessionProvider, useSession } from "../session/SessionContext";
import { SessionBar } from "../session/SessionBar";
import { makeClient, type ApiClient } from "../api/client";
import { QueueScreen } from "../queue/QueueScreen";
import { ReviewScreen } from "../review/ReviewScreen";
function Shell({ client }: { client: ApiClient }) {
  const { session } = useSession();
  const [openId, setOpenId] = useState<string | null>(null);
  return (
    <div style={{ display: "grid", gridTemplateRows: "auto 1fr", height: "100vh" }}>
      <div><h1 style={{ margin: 8 }}>TourDesk Console</h1><SessionBar /></div>
      <main style={{ padding: 8, position: "relative" }}>
        {!session ? <p>Enter a tenant id and token to begin.</p>
          : openId ? <ReviewScreen client={client} conversationId={openId} agent="me" onDone={() => setOpenId(null)} />
          : <QueueScreen client={client} onOpen={setOpenId} />}
      </main>
    </div>
  );
}
function AppInner({ client }: { client?: ApiClient }) {
  const { session } = useSession();
  const resolved = client ?? makeClient(() => session);
  return <Shell client={resolved} />;
}
export function App({ client }: { client?: ApiClient }) {
  return <SessionProvider><AppInner client={client} /></SessionProvider>;
}
```

- [ ] **Step 4: Keep `App.test.tsx` green** — the Task 1 heading test still holds (App renders `<h1>` regardless of session). No change needed unless it fails; if it does, wrap render in nothing (App self-provides the provider).

- [ ] **Step 5: Run the full suite** — `cd console && npm test && npm run typecheck && npm run lint` → ALL PASS.

- [ ] **Step 6: Accessibility pass (manual, fix inline)** — Tab reaches every action with visible focus (tokens `:focus-visible`); action buttons carry text + key hints; the unsupported-claims cue is text, not colour-only; panes have `aria-label`; toasts use `role="status"`/`role="alert"`. Commit any fixes.

- [ ] **Step 7: Commit**

```bash
git add console/src && git commit -m "feat(console): app router + session gate + a11y pass + smoke test (M7 console slice complete)"
```

---

### Task 8: Backend live smoke (manual verification)

**Files:** none.

- [ ] **Step 1:** Start the backend + seed one tenant and a queued case (reuse a queue E2E seed helper or store functions).
- [ ] **Step 2:** `cd console && npm run dev`; enter the tenant id + an HS256 token matching the backend's `SSO_HMAC_SECRET`; confirm the queue loads, claim a case, the panes render, and an action round-trips.
- [ ] **Step 3:** Record the result in `console/README.md` under "Verified against backend". If a field differs from `types.ts`, fix `types.ts` and re-run the unit suite. No code commit required beyond any `types.ts` fix.

---

## Self-Review

**Spec coverage:** FR-M7-01 queue → T4; FR-M7-02 claim/409 → T4; FR-M7-12 SLA/breach → T4; FR-M7-03 three panes → T5–6; FR-M7-04 citations (inline_citations resolve flag) + unsupported_claims warning → T5/T6; FR-M7-05 keyboard actions (the 4 real ones) → T6; FR-M7-07 booking panel degraded/withheld → T5; FR-M7-19 autonomy indicator (outcome/route/reasons, no level) → T5. Dev auth ① → T2; polling ② → T4; plain-text editor ③ → T6. Scaffold/a11y/smoke → T1, T7, T8.

**Gaps carried (backend-missing, surfaced not faked):** evidence has no `snippet` (show title/url); autonomy has no `level` (show outcome/route); thread messages have no timestamp (show from/direction); only 4 actions exist server-side (the other 4 M7-05 actions are a backend follow-up, `ActionKind` intentionally omits them). Each is recorded here and in `task-3-report.md`.

**Placeholder scan:** no TBD/TODO; every code step has real code against the real types.

**Type consistency:** all field access is snake_case per `console/src/api/types.ts`; `risk_class` treated as a number; `ActRequest` DTO camelCase mapped in `client.ts`; `ActionKind` is the 4-value union everywhere; `ReviewScreen` draft-region placeholder (T5) is replaced in T6.
