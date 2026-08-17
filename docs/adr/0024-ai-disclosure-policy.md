# 0024 — AI disclosure on all generated messages; distinct human-reviewed wording

- **Status:** Accepted (provisional — **needs legal counsel** + owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner + qualified lawyer)
- **PRD source:** OD-09, LEG-07/08/09, FR-M13-01/02, §13.2

## Context

EU AI Act Article 50 transparency obligations have applied since 2 August 2026: a system interacting with
people must disclose it is AI, and generated content carries marking duties; non-compliance exposure is
reported up to €15m / 3% of turnover. OD-09 asks whether a **human-reviewed, human-edited** message must
still carry an AI disclosure — legally arguable and commercially sensitive.

## Decision (provisional — confirm with counsel before go-live)

- Every message **generated and auto-sent** by the system carries a clear AI disclosure, configurable in
  wording/placement but **not removable below the legal minimum** for in-scope tenants (LEG-07, FR-M13-01).
- Where a human **materially reviews and takes responsibility** for a message, the disclosure wording **may
  differ** (LEG-08); the distinction (AI-sent vs human-owned) is **recorded per message** and defensible.
- Machine-readable marking of AI-generated content, plus a per-message record of model, prompt version and
  generation timestamp (LEG-09, FR-M13-02).

## Alternatives considered

- **Disclose identically on everything** — safest legally, but may over-disclose on human-owned replies and
  is commercially heavier; kept as the fallback if counsel advises.
- **Disclose on nothing** — rejected: non-compliant with Art. 50.

## Consequences

- Requires per-message provenance and disclosure text stored on `SentMessage`; feeds the AI-disclosure log
  (FR-M10-07).
- The human-reviewed wording variant (LEG-08) is the specific point requiring **legal sign-off** before
  go-live (§13.2 caveat: "not legal advice").
- Track the transparency Code of Practice and assess signing it (LEG-11).

## Sign-off needed

Qualified lawyer must confirm the LEG-08 distinction and the minimum wording; until then, default to the
disclose-on-all fallback.
