# Development environment

Tooling and backing services are managed with [`mise`](https://mise.jdx.dev). One config
([`mise.toml`](../mise.toml)) pins the toolchain and defines tasks that wrap the OSS services in
[`compose.yml`](../compose.yml). Stack rationale: [ADR-0030](adr/0030-technology-stack.md).

## Quickstart

```bash
mise trust          # trust this repo's mise.toml (first time only)
mise run setup      # install go+node, create .env, start services
mise run ps         # check they're up
```

That's it — `setup` installs the toolchain, copies `.env.example` → `.env`, and starts the services.

## Services (all self-hosted OSS — ADR-0030 / ADR-0018)

| Service | Image | Port(s) | Role | Spec |
|---|---|---|---|---|
| **postgres** | `paradedb/paradedb` | 5433 | Source of truth; RLS isolation; `pgvector` + `pg_search` (BM25) | ADR-0015, ADR-0012 |
| **nats** | `nats` (JetStream) | 4222, 8222 | Inter-service/-process bus **and** durable pipeline queue | ADR-0030, pipeline §3 |
| **tika** | `apache/tika:*-full` | 9998 | Document text extraction + OCR (Tesseract bundled) | FR-M4-02, FR-M1-09 |
| **clamav** | `clamav/clamav` | 3310 | Attachment malware scanning | SEC-07, FR-M1-09 |

The **only** external dependency not run locally is the LLM provider (ADR-0028) — set its keys in `.env`.

> First `clamav` start downloads the signature database and can take a few minutes to report healthy;
> the other services come up in seconds.

## Common tasks

```bash
mise run up          # start services            mise run down     # stop + remove (keeps volumes)
mise run ps          # status                    mise run logs     # tail all logs
mise run logs -- nats   # tail one service        mise run restart  # restart all
mise run db          # psql shell                 mise run nats:info   # JetStream server info
mise run clam:ping   # ping clamd                 mise run clean    # DESTROY data volumes
mise run doctor      # tools + service health
```

## App processes (once scaffolded)

The backend (`./backend`, Go) and console (`./console`, React/TS) are not scaffolded yet — the stack is
decided ([ADR-0030](adr/0030-technology-stack.md)) but implementation begins against the specs via the
[`spec-driven-dev`](../.claude/agents/spec-driven-dev.md) agent. `mise run backend` / `mise run frontend`
/ `mise run dev` are wired and will run those dirs as soon as they exist.

## Notes

- `.env` and `mise.local.toml` are git-ignored — put machine-specific overrides and real secrets there.
- Dev credentials in `mise.toml`/`.env.example` are throwaway (`tourdesk`/`tourdesk`); never reuse them
  outside local dev.
