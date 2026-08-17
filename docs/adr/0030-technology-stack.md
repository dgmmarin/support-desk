# 0030 — Technology stack: Go + Postgres + React, self-hosted OSS substrate

- **Status:** Accepted (provisional — needs owner sign-off; assumes a Go-capable team, OD-18)
- **Date:** 2026-08-17
- **Deciders:** TBD (engineering lead + product owner)
- **PRD source:** OD-15, OD-16, OD-17; builds on ADR-0019
- **Supersedes:** the "buy the substrate" framing of [ADR-0019](0019-buy-substrate-build-the-core.md) —
  clarified to *adopt self-hosted OSS*, not SaaS.

## Context

ADR-0019 settled *what* to build (pipeline, gate, console) vs. adopt (everything else) but left the
concrete stack open (OD-15/16/17). The constraints that actually drive the choice come from the specs:
a deterministic, safety-critical gate (ADR-0001); horizontally-scalable idempotent workers under a 10×
burst (NFR-S-01/04); data-layer tenant isolation (ADR-0015); hybrid semantic+BM25 retrieval (ADR-0012);
a model-agnostic, no-fine-tuning-in-v1 AI layer (ADR-0010, ADR-0008); a fast WCAG-AA console (M7); and
EU residency + PII minimisation + no-training (ADR-0018). The team does **not** want to buy SaaS for the
substrate.

Crucially, the usual reason to reach for Python — the RAG/ML ecosystem — is neutralised here: ADR-0010
makes models an HTTP interface and ADR-0008 bans fine-tuning in v1, so the AI work is API orchestration,
not in-process ML.

## Decision (provisional)

| Layer | Choice |
|---|---|
| Backend: pipeline, **gate**, connectors, APIs | **Go** — typed deterministic core, cheap high-concurrency workers, strong stdlib (`net/mail`, `net/http`) |
| Database + retrieval | **Postgres** — row-level security for isolation (ADR-0015); `pgvector` (semantic) + native FTS, with the OSS **ParadeDB `pg_search`** extension for true BM25 (ADR-0012) |
| Work queue | **River** (Postgres-backed, Go) or self-hosted NATS — at-least-once + idempotent (NFR-S-04) |
| LLM access | Provider **HTTP** behind a Go interface (ADR-0010); provider constrained by ADR-0028 |
| Embeddings | EU-resident provider API, or self-hosted multilingual model (multilingual-e5 class) for full residency |
| Document extraction / OCR | **Apache Tika** + **Tesseract**, self-hosted OSS containers (FR-M4-02, FR-M1-09) |
| Malware scanning | **ClamAV**, self-hosted OSS (SEC-07) |
| Web crawler | Built in Go (`colly`), so egress-allowlist/robots rules stay in our control (SEC-08) |
| Console | **React + TypeScript** (Vite) — three-pane review, live queue, keyboard-first, WCAG 2.2 AA (M7) |
| Eval / regression harness | Go to start; a thin **Python** service added later *only if* eval sophistication earns it (ADR-0013) |

Everything except the **LLM provider** runs inside our own EU infrastructure.

## Alternatives considered

- **Python (FastAPI) core** — best AI ecosystem and hiring, but weaker CPU-bound burst throughput and
  dynamic typing on the safety-critical gate; the v1 AI layer doesn't need it. Choose this only if the
  team is Python-native with no Go (OD-18).
- **TypeScript everywhere (NestJS + React)** — smallest cognitive surface, shared types; weaker at
  high-throughput deterministic workers than Go. Viable fallback for a tiny team.
- **Buy SaaS for search / extraction / OCR** — rejected: shipping attachments containing passport/card
  data to a third-party makes it a sub-processor and a PII surface; self-hosting OSS is *more* compliant
  (ADR-0018), not merely cheaper.

## Consequences

- One backend language around the code that must not be wrong (the gate) — the lazy-senior win.
- No SaaS sub-processors beyond the LLM provider; simplifies the DPA, sub-processor list (FR-M13-09) and
  DPIA (LEG-04).
- **Layout-aware table extraction** (FR-M4-02) is the main technical risk to spike early, because
  structured feeds (FR-M4-03) unlock the R0 auto-send intents (§8.3). Tika handles most formats; tables
  need tuning.
- Postgres is the single source of truth for isolation *and* retrieval; graduate to self-hosted
  OpenSearch only if retrieval outgrows PG — measured, not pre-emptive.
- Depends on OD-18 (a Go-capable team). If the team is Python-native, this ADR flips to the FastAPI
  alternative; Postgres/React/OSS-substrate are unaffected.

## Sign-off needed

Confirm the team has (or will hire for) Go, and ratify Postgres-first retrieval before OpenSearch.
