# AGENTS.md — TourDesk AI

Project-level instructions for any AI coding agent working in this repository. Read this first.

## What this project is

**TourDesk AI** — a multi-tenant B2B SaaS AI email support desk for tour operators. It reads an
operator's support mailbox, understands each email, retrieves grounded facts from the operator's own
content and reservation system, and either sends an answer automatically (only when provably safe) or
hands a drafted answer to a human reviewer. Full context: `PRD-AI-Support-Desk-for-Tour-Operators.md`.

## The docs are the contract

This project is **spec-driven**. Everything you build is governed by written specs and architecture
decisions. **When code and docs disagree, the docs win** — you either have a bug, or the docs need a
deliberate, surfaced change.

- **Start here:** `docs/README.md`
- **Specs (what to build):** `docs/specs/` — `00-overview.md`, `pipeline.md`, `data-model.md`, `nfr.md`,
  and one spec per module `M1`…`M13`. §2 of each module spec is the `FR-id → testable contract` table.
- **ADRs (which decision & why):** `docs/adr/` — index in `0000-index.md`. **Accepted** = settled;
  **Accepted (provisional)** (0019–0029) = adopts a PRD recommendation but *needs owner sign-off*;
  `0024` also needs legal counsel.
- **PRD (why):** `PRD-AI-Support-Desk-for-Tour-Operators.md`.

## How to work here

1. **Read the governing spec + its linked ADRs before writing any code.** Guessing the contract is the
   main failure mode. Grep the PRD for the relevant `FR-` ids.
2. **Test-first (red → green → refactor).** Write a failing test named for its requirement id
   (e.g. `test_FR_M6_02_...`) — including the **fail-closed path** — before implementation code. Trivial
   one-liners excepted; anything with logic gets a test.
3. **Minimum code to pass.** Lazy in the senior sense: reuse stdlib/platform/existing deps, delete over
   add, no unrequested abstractions. Never lazy about the invariants below, trust-boundary validation,
   error handling that prevents data loss, security, or accessibility.
4. **Verify with real output** before claiming done — paste the test run; never assert "passing" unseen.
5. **Trace every change** to the `FR-`/`SR-`/`NFR-`/`SEC-`/`LEG-` id it satisfies. No governing id ⇒ you
   may be adding scope; stop and ask.

## Non-negotiable invariants

These are safety, legal, and existential-security controls. Code that weakens one is wrong even if tests
pass. Full detail in the specs/ADRs; the short list:

- **Deterministic send gate** (ADR-0001) — a model never decides to send; the gate is deterministic code.
- **All 15 gate conditions pass** for auto-send (M6, §9.2). R2 ⇒ never auto-send; hard-stop ⇒ human;
  kill switch overrides everything.
- **Commitment guardrail** (ADR-0006) — no price/availability/fee/change/compensation unless it came from
  a system of record or a human; deterministic check, not a prompt.
- **Grounding + independent verifier** (ADR-0007) — answer only from cited sources; verifier is a separate
  model/call; insufficient grounding ⇒ abstain + escalate.
- **Content is data, never instructions** (ADR-0016) — customer text / attachments / crawled pages;
  egress allowlist only; injection ⇒ human.
- **Verification gates personal data** (ADR-0011) — disclosure matrix + DMARC; never disclose to a
  non-contact even with a correct reference; never reveal whether an address has a booking.
- **Data-layer tenant isolation** (ADR-0015) — every query tenant-scoped at the data layer; a query
  without tenant scope must fail. **Cross-tenant leak is P0.** Ship the isolation test with the code.
- **Live reads for time-critical facts** (FR-M12-05) — the booking cache is never the source of truth.
- **No fine-tuning; human-gated knowledge loop** (ADR-0008); **regression gate** on the frozen eval set
  (ADR-0013); **fail-closed / degrade, don't fail** (principle 7); **EU residency + PII masking**
  (ADR-0018); **auditability** — every sent message reconstructs its full chain.

## Stop and ask when

- The task depends on a **provisional** ADR (0019–0029) or an unresolved **open decision** (OD-*).
- Two specs/ADRs conflict, or a spec conflicts with the PRD.
- The work would require weakening an invariant above (never yours to trade away).
- The language / framework / substrate for the area isn't chosen yet (much is deferred — ADR-0019,
  OD-15/16/17). Confirm the stack before writing code that assumes one.

## The spec-driven-dev agent

For implementation, bugfixes, and refactors, use the project agent
[`.claude/agents/spec-driven-dev.md`](.claude/agents/spec-driven-dev.md) — it holds the full doc map,
the workflow above, and the invariants. Claude Code auto-discovers it at session start; invoke it with
`subagent_type: "spec-driven-dev"` or by name. This `AGENTS.md` is the shared baseline for any agent;
the agent file is the deeper, dispatchable version of the same contract.
