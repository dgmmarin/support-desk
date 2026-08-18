# M7 Agent Console Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the agent review console — a prioritised queue feeding a three-pane review view with inline citations, one-keystroke actions, and claim/lock — as a React+TS+Vite app over the existing `/queue/*` backend APIs.

**Architecture:** A small, framework-light SPA in `./console/`. A typed `api/` client wraps fetch (injects `X-Tenant-ID` + bearer, maps status codes to typed errors); a `session/` React context holds tenant+token set by a dev session bar; `queue/` polls and claims; `review/` renders the three panes + keyboard action bar. No state library, no component kit, no CSS framework — React context + local state + a polling hook + a thin `ui/` token layer.

**Tech Stack:** React 18, TypeScript 5, Vite 5, Vitest + @testing-library/react + jsdom, CSS modules + CSS custom-property tokens. No other runtime deps.

**Spec:** `docs/superpowers/specs/2026-08-18-m7-agent-console-design.md`

## Global Constraints

- **Location:** all code under `./console/` (ADR-0030 names the console `./console`). Do not modify `./backend` — if a field is missing, that is a separate backend issue, not a UI workaround.
- **Stack floors (ADR-0030):** React + TypeScript + Vite. WCAG 2.2 AA: full keyboard operation, visible focus, AA contrast, citation highlight never colour-only.
- **Auth/tenant:** every API call sends `X-Tenant-ID: <tenantId>` and `Authorization: Bearer <token>` from the session context (decision ①). Backend verifies HS256 and scopes by tenant.
- **Live updates:** polling only (decision ②), default interval 5000 ms, revalidate after each mutation. No WebSocket/SSE.
- **Draft editor:** plain `<textarea>` + citation overlay (decision ③). No rich-text library.
- **Test-first:** every logic unit gets a failing Vitest test first (repo norm). Commit after each green task. No co-authored/signed-off trailers (repo rule).
- **Field-name source of truth:** `src/api/types.ts` is the single place backend JSON shapes are pinned; reconcile against the Go `json:` tags in `backend/internal/queue/*.go` during Task 3.

---

### Task 1: Scaffold the console app

**Files:**
- Create: `console/package.json`, `console/tsconfig.json`, `console/vite.config.ts`, `console/vitest.setup.ts`, `console/index.html`, `console/src/main.tsx`, `console/src/app/App.tsx`, `console/src/ui/tokens.css`, `console/.gitignore`, `console/README.md`
- Test: `console/src/app/App.test.tsx`

**Interfaces:**
- Produces: a runnable Vite app whose root renders `<App/>`; `npm test`, `npm run build`, `npm run typecheck`, `npm run lint` scripts exist.

- [ ] **Step 1: Create `console/package.json`**

```json
{
  "name": "tourdesk-console",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "typecheck": "tsc --noEmit",
    "test": "vitest run",
    "test:watch": "vitest",
    "lint": "eslint src --max-warnings=0"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.4.8",
    "@testing-library/react": "^16.0.1",
    "@testing-library/user-event": "^14.5.2",
    "@types/react": "^18.3.5",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "eslint": "^9.9.1",
    "jsdom": "^25.0.0",
    "typescript": "^5.5.4",
    "vite": "^5.4.3",
    "vitest": "^2.0.5"
  }
}
```

- [ ] **Step 2: Create config files**

`console/tsconfig.json`:
```json
{
  "compilerOptions": {
    "target": "ES2022", "lib": ["ES2022", "DOM", "DOM.Iterable"], "module": "ESNext",
    "moduleResolution": "Bundler", "jsx": "react-jsx", "strict": true,
    "noUnusedLocals": true, "noUnusedParameters": true, "esModuleInterop": true,
    "skipLibCheck": true, "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src", "vitest.setup.ts"]
}
```
`console/vite.config.ts`:
```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/queue": "http://localhost:8080", "/analytics": "http://localhost:8080" } },
  test: { environment: "jsdom", globals: true, setupFiles: ["./vitest.setup.ts"] },
} as any);
```
`console/vitest.setup.ts`:
```ts
import "@testing-library/jest-dom/vitest";
```
`console/index.html`:
```html
<!doctype html><html lang="en"><head><meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>TourDesk Console</title></head>
<body><div id="root"></div><script type="module" src="/src/main.tsx"></script></body></html>
```
`console/.gitignore`:
```
node_modules
dist
```

- [ ] **Step 3: Write the failing test**

`console/src/app/App.test.tsx`:
```tsx
import { render, screen } from "@testing-library/react";
import { App } from "./App";
test("renders the console shell heading", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: /tourdesk console/i })).toBeInTheDocument();
});
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd console && npm install && npm test`
Expected: FAIL — cannot resolve `./App`.

- [ ] **Step 5: Create tokens, App, and entrypoint**

`console/src/ui/tokens.css`:
```css
:root {
  --bg: #ffffff; --fg: #14161a; --muted: #5b6472; --line: #d7dce3;
  --accent: #1f6feb; --danger: #b42318; --warn: #9a6700; --ok: #067647;
  --focus: #1f6feb; --radius: 6px; --pane-gap: 12px;
  font-family: system-ui, sans-serif; color: var(--fg); background: var(--bg);
}
*:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
@media (prefers-color-scheme: dark) {
  :root { --bg:#0f1216; --fg:#e6e9ee; --muted:#9aa4b2; --line:#2a313b; }
}
```
`console/src/app/App.tsx`:
```tsx
import "../ui/tokens.css";
export function App() {
  return (
    <div>
      <header><h1>TourDesk Console</h1></header>
      <main>{/* router mounted in Task 7 */}</main>
    </div>
  );
}
```
`console/src/main.tsx`:
```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app/App";
createRoot(document.getElementById("root")!).render(<StrictMode><App /></StrictMode>);
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd console && npm test`
Expected: PASS. Also `npm run typecheck` → clean.

- [ ] **Step 7: Write `console/README.md`** (dev-grade auth + polling recorded as intentional)

```md
# TourDesk Console (M7)
React+TS+Vite console over the Go backend `/queue/*` APIs.
- Dev: `npm install && npm run dev` (proxies /queue, /analytics to :8080).
- Auth is dev-grade: paste/generate an HS256 bearer + tenant id in the session bar (decision ①).
- Queue updates by polling every 5s (decision ②). Draft editor is plain-text (decision ③).
- Production SSO redirect, real-time push, and rich-text are later slices.
Test: `npm test`. Typecheck: `npm run typecheck`.
```

- [ ] **Step 8: Commit**

```bash
git add console && git commit -m "feat(console): scaffold Vite+React+TS app shell (M7)"
```

---

### Task 2: Session context + dev session bar

**Files:**
- Create: `console/src/session/SessionContext.tsx`, `console/src/session/SessionBar.tsx`
- Test: `console/src/session/SessionContext.test.tsx`

**Interfaces:**
- Produces:
  - `type Session = { tenantId: string; token: string }`
  - `useSession(): { session: Session | null; setSession: (s: Session) => void; clear: () => void }`
  - `<SessionProvider>` (wraps app; persists to `sessionStorage` key `td.session`)
  - `<SessionBar/>` (inputs for tenant id + token, Save/Clear)

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionProvider, useSession } from "./SessionContext";
function Probe() {
  const { session, setSession } = useSession();
  return (<div>
    <span data-testid="tenant">{session?.tenantId ?? "none"}</span>
    <button onClick={() => setSession({ tenantId: "t1", token: "jwt" })}>set</button>
  </div>);
}
test("stores and exposes the session", async () => {
  render(<SessionProvider><Probe /></SessionProvider>);
  expect(screen.getByTestId("tenant")).toHaveTextContent("none");
  await userEvent.click(screen.getByText("set"));
  expect(screen.getByTestId("tenant")).toHaveTextContent("t1");
  expect(JSON.parse(sessionStorage.getItem("td.session")!).tenantId).toBe("t1");
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd console && npm test -- SessionContext`
Expected: FAIL — cannot resolve `./SessionContext`.

- [ ] **Step 3: Implement `SessionContext.tsx`**

```tsx
import { createContext, useContext, useMemo, useState, ReactNode } from "react";
export type Session = { tenantId: string; token: string };
type Ctx = { session: Session | null; setSession: (s: Session) => void; clear: () => void };
const SessionCtx = createContext<Ctx | null>(null);
const KEY = "td.session";
function load(): Session | null { try { return JSON.parse(sessionStorage.getItem(KEY) ?? "null"); } catch { return null; } }
export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setS] = useState<Session | null>(load);
  const value = useMemo<Ctx>(() => ({
    session,
    setSession: (s) => { sessionStorage.setItem(KEY, JSON.stringify(s)); setS(s); },
    clear: () => { sessionStorage.removeItem(KEY); setS(null); },
  }), [session]);
  return <SessionCtx.Provider value={value}>{children}</SessionCtx.Provider>;
}
export function useSession(): Ctx {
  const c = useContext(SessionCtx);
  if (!c) throw new Error("useSession outside SessionProvider");
  return c;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd console && npm test -- SessionContext`
Expected: PASS.

- [ ] **Step 5: Implement `SessionBar.tsx`** (no test beyond the context; it is thin glue)

```tsx
import { useState } from "react";
import { useSession } from "./SessionContext";
export function SessionBar() {
  const { session, setSession, clear } = useSession();
  const [tenantId, setTenant] = useState(session?.tenantId ?? "");
  const [token, setToken] = useState(session?.token ?? "");
  return (
    <form aria-label="dev session" onSubmit={(e) => { e.preventDefault(); setSession({ tenantId, token }); }}
          style={{ display: "flex", gap: 8, alignItems: "center", padding: 8, borderBottom: "1px solid var(--line)" }}>
      <label>Tenant <input value={tenantId} onChange={(e) => setTenant(e.target.value)} required /></label>
      <label>Token <input value={token} onChange={(e) => setToken(e.target.value)} type="password" required style={{ width: 220 }} /></label>
      <button type="submit">Save</button>
      {session && <button type="button" onClick={clear}>Clear</button>}
      {session && <span aria-live="polite" style={{ color: "var(--muted)" }}>tenant {session.tenantId}</span>}
    </form>
  );
}
```

- [ ] **Step 6: Commit**

```bash
git add console/src/session && git commit -m "feat(console): session context + dev session bar (decision ①)"
```

---

### Task 3: Typed API client + response types

**Files:**
- Create: `console/src/api/types.ts`, `console/src/api/client.ts`
- Test: `console/src/api/client.test.ts`
- Read (to reconcile field names): `backend/internal/queue/*.go` (`json:` tags on QueueItem / review surface / act result)

**Interfaces:**
- Produces:
  - Types: `QueueItem`, `Sla`, `ReviewSurface`, `Citation`, `BookingPanel`, `AutonomyIndicator`, `ActResult`, `ActionKind`.
  - `class ApiError extends Error { status: number }`, plus `PermissionError`(403), `ClaimConflict`(409), `MissingTenant`(400) subclasses.
  - `makeClient(getSession: () => Session | null)` → `{ getQueue(), claim(id), resolve(id), getReview(id), act(req) }` returning typed promises.

- [ ] **Step 1: Reconcile field names**

Open `backend/internal/queue/queue.go`, `review.go`, `act.go`; note the exact `json:` tags. If any differ from the shapes below (e.g. `conversation_id` vs `conversationId`), adjust `types.ts` to match the Go tags. The shapes below reflect the reported backend design; the Go tags are authoritative.

- [ ] **Step 2: Write `types.ts`**

```ts
export type Sla = { defined: boolean; targetAt?: string; remainingSecs: number; windowSecs: number; breached: boolean };
export type QueueItem = {
  conversationId: string; score: number; riskClass: string; intent: string; channel: string;
  enqueuedAt: string; departureAt?: string; sla: Sla; locked: boolean; claimedBy?: string; status: string; queue: string;
};
export type Citation = { claimSpan: string; knowledgeItemId?: string; bookingFieldPath?: string; score?: number };
export type BookingPanel = { available: boolean; reason?: string; fields?: Record<string, string> };
export type AutonomyIndicator = { level: string; autoSendEligible: boolean; confidenceBand?: string; reasonsForAgent: string[] };
export type ReviewSurface = {
  conversationId: string; customerThread: { from: string; sentAt: string; body: string }[];
  draftBody: string | null; abstainReason?: string; citations: Citation[];
  evidenceSources: { id: string; title: string; snippet: string }[];
  booking: BookingPanel; autonomy: AutonomyIndicator;
  translation: { mtAvailable: boolean; customerLanguage: string; draftLanguage: string };
};
export type ActionKind = "approve_send" | "edit_send" | "reject" | "escalate" | "snooze" | "reassign" | "mark_spam" | "request_info";
export type ActRequest = { conversationId: string; agent: string; action: ActionKind; editedBody?: string };
export type ActResult = { action: ActionKind; sent: boolean; message?: string };
```

- [ ] **Step 3: Write the failing test**

```ts
import { makeClient, ClaimConflict, MissingTenant } from "./client";
const session = { tenantId: "t1", token: "jwt" };
function stubFetch(status: number, body: unknown) {
  return async () => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}
test("getQueue returns items and sends auth headers", async () => {
  let seen: Request | undefined;
  const fetchImpl = async (input: any, init: any) => { seen = new Request(input, init); return new Response(JSON.stringify({ items: [{ conversationId: "c1" }] }), { status: 200 }); };
  const c = makeClient(() => session, fetchImpl as any);
  const items = await c.getQueue();
  expect(items[0].conversationId).toBe("c1");
  expect(seen!.headers.get("X-Tenant-ID")).toBe("t1");
  expect(seen!.headers.get("Authorization")).toBe("Bearer jwt");
});
test("claim maps 409 to ClaimConflict", async () => {
  const c = makeClient(() => session, stubFetch(409, { error: "claimed" }) as any);
  await expect(c.claim("c1")).rejects.toBeInstanceOf(ClaimConflict);
});
test("missing session throws MissingTenant before fetch", async () => {
  const c = makeClient(() => null, stubFetch(200, {}) as any);
  await expect(c.getQueue()).rejects.toBeInstanceOf(MissingTenant);
});
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd console && npm test -- api/client`
Expected: FAIL — cannot resolve `./client`.

- [ ] **Step 5: Implement `client.ts`**

```ts
import type { Session } from "../session/SessionContext";
import type { QueueItem, ReviewSurface, ActRequest, ActResult } from "./types";
export class ApiError extends Error { constructor(public status: number, msg: string) { super(msg); } }
export class PermissionError extends ApiError {}
export class ClaimConflict extends ApiError {}
export class MissingTenant extends ApiError {}
type FetchImpl = typeof fetch;
export function makeClient(getSession: () => Session | null, fetchImpl: FetchImpl = fetch) {
  async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
    const s = getSession();
    if (!s?.tenantId || !s?.token) throw new MissingTenant(400, "no session");
    const res = await fetchImpl(path, {
      method,
      headers: { "X-Tenant-ID": s.tenantId, "Authorization": `Bearer ${s.token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.status === 403) throw new PermissionError(403, "insufficient role");
    if (res.status === 409) throw new ClaimConflict(409, "already claimed");
    if (res.status === 400) throw new MissingTenant(400, "missing tenant/bad request");
    if (!res.ok) throw new ApiError(res.status, `request failed (${res.status})`);
    return (res.status === 204 ? undefined : await res.json()) as T;
  }
  return {
    getQueue: async (): Promise<QueueItem[]> => (await call<{ items: QueueItem[] }>("GET", "/queue")).items ?? [],
    claim: (conversationId: string, agent = "me") => call<{ expires_at: string }>("POST", "/queue/claim", { conversation_id: conversationId, agent }),
    resolve: (conversationId: string, agent = "me") => call<void>("POST", "/queue/resolve", { conversation_id: conversationId, agent }),
    getReview: (conversationId: string) => call<ReviewSurface>("GET", `/queue/review?conversation_id=${encodeURIComponent(conversationId)}`),
    act: (req: ActRequest) => call<ActResult>("POST", "/queue/act", { conversation_id: req.conversationId, agent: req.agent, action: req.action, edited_body: req.editedBody }),
  };
}
export type ApiClient = ReturnType<typeof makeClient>;
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd console && npm test -- api/client`
Expected: PASS (3 tests).

- [ ] **Step 7: Commit**

```bash
git add console/src/api && git commit -m "feat(console): typed API client + response types + error mapping"
```

---

### Task 4: Queue screen (poll, scored rows, SLA/breach, claim + 409)

**Files:**
- Create: `console/src/queue/usePolling.ts`, `console/src/queue/QueueScreen.tsx`
- Test: `console/src/queue/QueueScreen.test.tsx`

**Interfaces:**
- Consumes: `ApiClient` (Task 3), `useSession` (Task 2).
- Produces: `<QueueScreen client={ApiClient} onOpen={(id: string) => void} />`; `usePolling(fn, ms, deps)`.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueueScreen } from "./QueueScreen";
import { ClaimConflict } from "../api/client";
const items = [
  { conversationId: "c1", score: 9, riskClass: "R1", intent: "faq", channel: "email", enqueuedAt: "", sla: { defined: true, remainingSecs: -60, windowSecs: 3600, breached: true }, locked: false, status: "pending", queue: "normal" },
  { conversationId: "c2", score: 3, riskClass: "R0", intent: "faq", channel: "email", enqueuedAt: "", sla: { defined: true, remainingSecs: 1800, windowSecs: 3600, breached: false }, locked: false, status: "pending", queue: "normal" },
];
function client(overrides = {}) {
  return { getQueue: async () => items, claim: async () => ({ expires_at: "" }), resolve: async () => {}, getReview: async () => ({} as any), act: async () => ({} as any), ...overrides } as any;
}
test("renders rows ordered by score with a breach badge", async () => {
  render(<QueueScreen client={client()} onOpen={() => {}} />);
  const rows = await screen.findAllByRole("row");
  // header + 2 data rows; first data row is highest score c1 and shows breach
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

- [ ] **Step 2: Run test to verify it fails**

Run: `cd console && npm test -- QueueScreen`
Expected: FAIL — cannot resolve `./QueueScreen`.

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

- [ ] **Step 4: Implement `QueueScreen.tsx`**

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
            <tr key={it.conversationId}>
              <td>{it.conversationId}</td><td>{it.score.toFixed(1)}</td><td>{it.riskClass}</td><td>{it.intent}</td>
              <td>{it.sla.breached
                ? <span style={{ color: "var(--danger)", fontWeight: 600 }}>⚠ breach</span>
                : it.sla.defined ? `${Math.round(it.sla.remainingSecs / 60)}m` : "—"}</td>
              <td><button onClick={() => claim(it.conversationId)} aria-label={`claim ${it.conversationId}`}>Claim</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd console && npm test -- QueueScreen`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit**

```bash
git add console/src/queue && git commit -m "feat(console): queue screen — polling, scored rows, SLA breach, claim 409"
```

---

### Task 5: Review screen — thread + evidence (citations, booking, autonomy)

**Files:**
- Create: `console/src/review/ThreadPane.tsx`, `console/src/review/EvidencePane.tsx`, `console/src/review/ReviewScreen.tsx`
- Test: `console/src/review/EvidencePane.test.tsx`, `console/src/review/ReviewScreen.test.tsx`

**Interfaces:**
- Consumes: `ApiClient.getReview` (Task 3), types from `api/types`.
- Produces: `<ReviewScreen client={ApiClient} conversationId={string} agent={string} onDone={() => void} />`; `<EvidencePane surface={ReviewSurface} highlighted={string|null} />`.

- [ ] **Step 1: Write the failing test for EvidencePane**

```tsx
import { render, screen } from "@testing-library/react";
import { EvidencePane } from "./EvidencePane";
const surface = {
  conversationId: "c1", customerThread: [], draftBody: "You depart 09:00.", abstainReason: undefined,
  citations: [{ claimSpan: "You depart 09:00.", bookingFieldPath: "itinerary.departure" }],
  evidenceSources: [{ id: "k1", title: "FAQ", snippet: "..." }],
  booking: { available: false, reason: "connector down" },
  autonomy: { level: "L1", autoSendEligible: false, reasonsForAgent: ["G05 confidence below threshold"] },
  translation: { mtAvailable: false, customerLanguage: "", draftLanguage: "en" },
} as any;
test("shows degraded booking and the autonomy reasons", () => {
  render(<EvidencePane surface={surface} highlighted={null} />);
  expect(screen.getByText(/booking data unavailable/i)).toBeInTheDocument();
  expect(screen.getByText(/G05 confidence below threshold/)).toBeInTheDocument();
});
test("renders a source list", () => {
  render(<EvidencePane surface={surface} highlighted={null} />);
  expect(screen.getByText("FAQ")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd console && npm test -- EvidencePane`
Expected: FAIL — cannot resolve `./EvidencePane`.

- [ ] **Step 3: Implement `ThreadPane.tsx` and `EvidencePane.tsx`**

`ThreadPane.tsx`:
```tsx
import type { ReviewSurface } from "../api/types";
export function ThreadPane({ thread }: { thread: ReviewSurface["customerThread"] }) {
  return (
    <div aria-label="customer thread" style={{ overflow: "auto" }}>
      {thread.length === 0 ? <p style={{ color: "var(--muted)" }}>No prior messages.</p>
        : thread.map((m, i) => (
          <article key={i} style={{ borderBottom: "1px solid var(--line)", padding: 8 }}>
            <div style={{ color: "var(--muted)", fontSize: 12 }}>{m.from} · {m.sentAt}</div>
            <div>{m.body}</div>
          </article>))}
    </div>
  );
}
```
`EvidencePane.tsx`:
```tsx
import type { ReviewSurface } from "../api/types";
export function EvidencePane({ surface, highlighted }: { surface: ReviewSurface; highlighted: string | null }) {
  const { booking, autonomy, evidenceSources, citations } = surface;
  return (
    <aside aria-label="evidence" style={{ overflow: "auto", display: "grid", gap: "var(--pane-gap)" }}>
      <section aria-label="autonomy">
        <h3>Autonomy — {autonomy.level}{autonomy.confidenceBand ? ` · ${autonomy.confidenceBand}` : ""}</h3>
        {autonomy.autoSendEligible ? <p>Auto-send eligible.</p>
          : <ul>{autonomy.reasonsForAgent.map((r, i) => <li key={i}>{r}</li>)}</ul>}
      </section>
      <section aria-label="booking">
        <h3>Booking</h3>
        {booking.available
          ? <dl>{Object.entries(booking.fields ?? {}).map(([k, v]) => <div key={k}><dt style={{ color: "var(--muted)" }}>{k}</dt><dd>{v}</dd></div>)}</dl>
          : <p style={{ color: "var(--muted)" }}>booking data unavailable{booking.reason ? ` (${booking.reason})` : ""}</p>}
      </section>
      <section aria-label="sources">
        <h3>Cited sources</h3>
        <ul>{evidenceSources.map((s) => {
          const cited = citations.some((c) => c.knowledgeItemId === s.id);
          return <li key={s.id} style={cited && highlighted && s.id === highlighted ? { background: "var(--warn)", color: "#fff" } : undefined}>{s.title}</li>;
        })}</ul>
      </section>
    </aside>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd console && npm test -- EvidencePane`
Expected: PASS (2 tests).

- [ ] **Step 5: Write the failing test for ReviewScreen (abstain fallback)**

```tsx
import { render, screen } from "@testing-library/react";
import { ReviewScreen } from "./ReviewScreen";
function client(surface: any) { return { getReview: async () => surface, act: async () => ({}) } as any; }
test("shows abstained reason when there is no draft", async () => {
  const s = { conversationId: "c1", customerThread: [], draftBody: null, abstainReason: "escalated: hard-stop",
    citations: [], evidenceSources: [], booking: { available: false }, autonomy: { level: "L0", autoSendEligible: false, reasonsForAgent: [] }, translation: { mtAvailable: false, customerLanguage: "", draftLanguage: "" } };
  render(<ReviewScreen client={client(s)} conversationId="c1" agent="me" onDone={() => {}} />);
  expect(await screen.findByText(/escalated: hard-stop/)).toBeInTheDocument();
});
```

- [ ] **Step 6: Implement `ReviewScreen.tsx`** (loads the surface; renders 3 panes; draft/actions arrive in Task 6 — this task renders the abstain fallback + panes, and a placeholder action region)

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
      <ThreadPane thread={surface.customerThread} />
      <div aria-label="draft">
        {surface.draftBody === null
          ? <p role="status" style={{ color: "var(--warn)" }}>{surface.abstainReason ?? "abstained / escalated"}</p>
          : <div data-testid="draft-region">{/* DraftEditor + ActionBar mounted in Task 6 */}{surface.draftBody}</div>}
      </div>
      <EvidencePane surface={surface} highlighted={null} />
      <button onClick={onDone} style={{ position: "absolute", right: 8, top: 8 }}>Back to queue</button>
    </div>
  );
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd console && npm test -- review`
Expected: PASS.

- [ ] **Step 8: Commit**

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
- Consumes: `ApiClient.act` (Task 3), `ReviewSurface` (Task 3).
- Produces: `<DraftEditor value onChange citations />`; `<ActionBar onAct={(action: ActionKind) => void} busy />` with key bindings (a=approve_send, e=edit_send, x=escalate, r=reject).

- [ ] **Step 1: Write the failing test for ActionBar (keyboard triggers act)**

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

- [ ] **Step 2: Run test to verify it fails**

Run: `cd console && npm test -- ActionBar`
Expected: FAIL — cannot resolve `./ActionBar`.

- [ ] **Step 3: Implement `ActionBar.tsx` and `DraftEditor.tsx`**

`ActionBar.tsx`:
```tsx
import { useEffect } from "react";
import type { ActionKind } from "../api/types";
const KEYS: Record<string, ActionKind> = { a: "approve_send", e: "edit_send", x: "escalate", r: "reject" };
const LABEL: Record<ActionKind, string> = { approve_send: "Approve & send (a)", edit_send: "Edit & send (e)", escalate: "Escalate (x)", reject: "Reject (r)", snooze: "Snooze", reassign: "Reassign", mark_spam: "Mark spam", request_info: "Request info" };
export function ActionBar({ onAct, busy }: { onAct: (a: ActionKind) => void; busy: boolean }) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "TEXTAREA" || tag === "INPUT") return; // don't hijack typing
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
`DraftEditor.tsx`:
```tsx
import type { Citation } from "../api/types";
export function DraftEditor({ value, onChange, citations }: { value: string; onChange: (v: string) => void; citations: Citation[] }) {
  const uncited = value.trim().length > 0 && citations.length === 0;
  return (
    <div style={{ display: "grid", gap: 4 }}>
      {uncited && <p role="status" style={{ color: "var(--warn)" }}>⚠ No citations — sentences are unsupported.</p>}
      <textarea aria-label="draft reply" value={value} onChange={(e) => onChange(e.target.value)} rows={16} style={{ width: "100%", fontFamily: "inherit" }} />
    </div>
  );
}
```

- [ ] **Step 4: Write the DraftEditor test**

```tsx
import { render, screen } from "@testing-library/react";
import { DraftEditor } from "./DraftEditor";
test("warns when a non-empty draft has no citations", () => {
  render(<DraftEditor value="Hello." onChange={() => {}} citations={[]} />);
  expect(screen.getByText(/unsupported/i)).toBeInTheDocument();
});
```

- [ ] **Step 5: Wire them into `ReviewScreen.tsx` draft region**

Replace the `data-testid="draft-region"` block with:
```tsx
<DraftRegion client={client} surface={surface} agent={agent} onDone={onDone} />
```
and add this component at the bottom of `ReviewScreen.tsx` (imports: `useState`, `DraftEditor`, `ActionBar`, `ActionKind`, `ClaimConflict`/`ApiError`):
```tsx
function DraftRegion({ client, surface, agent, onDone }: { client: ApiClient; surface: ReviewSurface; agent: string; onDone: () => void }) {
  const [body, setBody] = useState(surface.draftBody ?? "");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  async function act(action: ActionKind) {
    setBusy(true); setMsg(null);
    try {
      const edited = action === "edit_send" ? body : undefined;
      const r = await client.act({ conversationId: surface.conversationId, agent, action, editedBody: edited });
      setMsg(r.sent ? "sent" : (r.message ?? "done"));
      onDone();
    } catch (e) {
      setMsg(e instanceof Error && e.message.includes("503") ? "sending unavailable — escalate instead" : "action failed");
    } finally { setBusy(false); }
  }
  return (<div>
    {msg && <p role="status">{msg}</p>}
    <DraftEditor value={body} onChange={setBody} citations={surface.citations} />
    <ActionBar onAct={act} busy={busy} />
  </div>);
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd console && npm test -- review`
Expected: PASS (ActionBar + DraftEditor + prior review tests).

- [ ] **Step 7: Commit**

```bash
git add console/src/review && git commit -m "feat(console): draft editor + keyboard action bar wired to /queue/act (G14 respected server-side)"
```

---

### Task 7: App router, session gate, accessibility pass, smoke test

**Files:**
- Modify: `console/src/app/App.tsx` (router: session gate → queue ⇄ review, mount SessionBar)
- Create: `console/src/app/App.smoke.test.tsx`
- Modify: `console/src/app/App.test.tsx` (wrap in SessionProvider)

**Interfaces:**
- Consumes: everything above.
- Produces: a complete app: no session → SessionBar prompt; session → QueueScreen; open → ReviewScreen; back → QueueScreen.

- [ ] **Step 1: Write the failing smoke test**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "./App";
test("with a session, shows the queue and opens a case", async () => {
  const client = {
    getQueue: async () => [{ conversationId: "c1", score: 5, riskClass: "R0", intent: "faq", channel: "email", enqueuedAt: "", sla: { defined: false, remainingSecs: 0, windowSecs: 0, breached: false }, locked: false, status: "pending", queue: "normal" }],
    claim: async () => ({ expires_at: "" }), resolve: async () => {},
    getReview: async () => ({ conversationId: "c1", customerThread: [], draftBody: "Hi.", citations: [], evidenceSources: [], booking: { available: false }, autonomy: { level: "L1", autoSendEligible: false, reasonsForAgent: [] }, translation: { mtAvailable: false, customerLanguage: "", draftLanguage: "en" } }),
    act: async () => ({ action: "approve_send", sent: true }),
  } as any;
  sessionStorage.setItem("td.session", JSON.stringify({ tenantId: "t1", token: "jwt" }));
  render(<App client={client} />);
  await userEvent.click(await screen.findByRole("button", { name: /claim c1/i }));
  expect(await screen.findByLabelText("draft reply")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd console && npm test -- App.smoke`
Expected: FAIL — `App` does not accept `client`, no routing.

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
export function App({ client }: { client?: ApiClient }) {
  return (
    <SessionProvider>
      <AppInner client={client} />
    </SessionProvider>
  );
}
function AppInner({ client }: { client?: ApiClient }) {
  const { session } = useSession();
  const resolved = client ?? makeClient(() => session);
  return <Shell client={resolved} />;
}
```

Note: `makeClient(() => session)` re-reads session on each call; passing an explicit `client` is for tests.

- [ ] **Step 4: Update `App.test.tsx`** (Task 1 test now needs the provider — heading assertion still valid)

```tsx
import { render, screen } from "@testing-library/react";
import { App } from "./App";
test("renders the console shell heading", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: /tourdesk console/i })).toBeInTheDocument();
});
```

- [ ] **Step 5: Run the full test suite**

Run: `cd console && npm test && npm run typecheck`
Expected: ALL PASS, typecheck clean.

- [ ] **Step 6: Accessibility pass (manual checklist, fix inline)**

Verify and fix: every actionable element reachable by Tab with visible focus (tokens `:focus-visible` covers it); action buttons have text labels + key hints; the citation/unsupported cue is text ("⚠ … unsupported"), not colour-only; panes have `aria-label`; toasts use `role="status"`/`role="alert"`. Commit any fixes.

- [ ] **Step 7: Commit**

```bash
git add console/src && git commit -m "feat(console): app router + session gate + a11y pass + smoke test (M7 console slice complete)"
```

---

### Task 8: Backend live smoke (optional gate, manual)

**Files:** none (verification only).

- [ ] **Step 1:** Start the backend (`cd backend && <run>`; services via compose already up) and seed one tenant + a queued case (reuse a queue E2E seed helper or the store functions).
- [ ] **Step 2:** `cd console && npm run dev`; open the browser, enter the tenant id + an HS256 token the backend accepts (matching `SSO_HMAC_SECRET`), confirm the queue loads, claim a case, the review panes render, and an action round-trips (approve→send once, or escalate).
- [ ] **Step 3:** Record the result in `console/README.md` under a "Verified against backend" note. No commit of code; this is a live-boundary check that the API contracts in `types.ts` match reality. If a field name differs, fix `types.ts` (Task 3) and re-run the unit suite.

---

## Self-Review

**Spec coverage:**
- FR-M7-01 queue → Task 4. FR-M7-02 claim/lock/409 → Task 4. FR-M7-12 SLA/breach → Task 4.
- FR-M7-03 three panes → Tasks 5–6. FR-M7-04 citations/unsupported → Tasks 5 (source highlight) + 6 (unsupported warning). FR-M7-05 keyboard actions → Task 6. FR-M7-07 booking panel degraded → Task 5. FR-M7-19 autonomy indicator → Task 5.
- Dev auth ① → Task 2. Polling ② → Task 4. Plain-text editor ③ → Task 6. Scaffold/a11y/smoke → Tasks 1, 7, 8.
- Out-of-scope items (search/notes/crisis/dashboards/SSO/push/rich-text) correctly absent.

**Placeholder scan:** no TBD/TODO; every code step has real code; the only "read the Go tags" step (Task 3.1) is a legitimate reconciliation, with a concrete default shape provided.

**Type consistency:** `ApiClient`, `QueueItem`, `ReviewSurface`, `Citation`, `ActionKind`, `ActRequest`/`ActResult`, `Session`, `usePolling`, `makeClient(getSession, fetchImpl?)` used identically across tasks. `act()` sends `conversation_id`/`edited_body` (snake) to the backend while the TS type uses `conversationId`/`editedBody` (camel) — the mapping lives only in `client.ts`.

**Known reconciliation point:** backend JSON casing (camel vs snake) is confirmed in Task 3 Step 1 against `backend/internal/queue/*.go`; `types.ts` is the single edit site if it differs.
