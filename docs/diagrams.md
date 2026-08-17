# System Diagrams — TourDesk AI

Derived from the [PRD](../PRD-AI-Support-Desk-for-Tour-Operators.md) and the
[specs](specs/00-overview.md). The Mermaid blocks below render on GitHub and in most Markdown viewers.
Each diagram's source also lives as a standalone `.mmd` file in [`diagrams/`](diagrams/) (with a
pre-rendered `.svg`), validated via Kroki.

| # | Diagram | Source | Governing docs |
|---|---|---|---|
| 1 | System architecture | [`01-system-architecture.mmd`](diagrams/01-system-architecture.mmd) | [overview](specs/00-overview.md) |
| 2 | Case pipeline (10 stages) | [`02-pipeline.mmd`](diagrams/02-pipeline.mmd) | [pipeline](specs/pipeline.md), [ADR-0002](adr/0002-ten-stage-failing-closed-pipeline.md) |
| 3 | Autonomy gate | [`03-autonomy-gate.mmd`](diagrams/03-autonomy-gate.mmd) | [M6](specs/M6-autonomy-gate.md), [ADR-0001](adr/0001-deterministic-send-gate.md) |
| 4 | Trust ladder | [`04-trust-ladder.mmd`](diagrams/04-trust-ladder.mmd) | [M6](specs/M6-autonomy-gate.md), [ADR-0004](adr/0004-trust-ladder-state-machine.md) |
| 5 | Core data model | [`05-data-model.mmd`](diagrams/05-data-model.mmd) | [data-model](specs/data-model.md), [ADR-0015](adr/0015-data-layer-tenant-isolation.md) |

---

## 1. System architecture

The whole platform on one page: customer email arrives at the mailbox, the pipeline enriches it against
the knowledge platform, the reservation connector (read-only) and the identity module, then the
**deterministic gate** decides between auto-send and the human console. Everything is per-tenant isolated
and EU-resident; the learning loop feeds approved answers back into knowledge.

```mermaid
flowchart TB
  subgraph ext["External"]
    CUST["Customer"]
    MB["Support mailbox<br/>IMAP · MS Graph · Gmail"]
    RES["Reservation system<br/>operator back-end"]
    SRC["Operator content<br/>website · docs · feeds"]
  end

  subgraph plat["TourDesk AI platform — multi-tenant, EU-resident"]
    direction TB
    MAIL["Mail provider adapter<br/>M1: parse · thread · loop-guard"]
    PIPE["Case pipeline<br/>10 stages, fail-closed"]
    IDV["Identity and verification<br/>M2: levels · disclosure matrix"]
    KNOW["Knowledge platform<br/>M4: hybrid retrieval · authority · freshness"]
    CONN["Reservation connector<br/>M12: read-only interface"]
    GATE{"Autonomy gate<br/>M6: deterministic · 15 conditions"}
    CONSOLE["Agent console<br/>M7: queue · review · audit"]
    LEARN["Learning loop<br/>M8: edits · eval set · regression gate"]
    ANALYTICS["Analytics and compliance<br/>M10/M13: ROI · disclosure · DSAR"]
    STORE[("Per-tenant stores<br/>cases · knowledge index · audit")]
  end

  CUST -->|email| MB --> MAIL --> PIPE
  PIPE <--> IDV
  PIPE <--> KNOW
  PIPE <--> CONN
  PIPE --> GATE
  GATE -->|auto_send| MAIL
  GATE -->|human_review / abstain| CONSOLE
  CONSOLE -->|approved send| MAIL
  MAIL -->|reply| CUST
  CONN <-->|read-only| RES
  SRC --> KNOW
  CONSOLE --> LEARN --> KNOW
  PIPE --> STORE
  CONSOLE --> STORE
  STORE --> ANALYTICS
  PIPE -. telemetry .-> ANALYTICS
```

---

## 2. Case pipeline (10 stages)

The spine. Ten discrete, observable stages that each **fail closed** (route to a human or quarantine,
never auto-send). Model calls are confined to the blue stages (3 Understand, 6 Generate, 7 Verify);
the green stages (2 Screen, 4 Identify, **8 Gate**) are deterministic — the send decision is code.

```mermaid
flowchart TB
  IN["Inbound email"] --> S1
  S1["1 Ingest<br/>parse · thread · dedupe · loop-check · scan"]
  S2["2 Screen<br/>spam · auto-reply · injection · DMARC"]
  S3["3 Understand<br/>language · intent · entities · risk · hard-stops"]
  S4["4 Identify<br/>booking resolution · verification level"]
  S5["5 Retrieve<br/>hybrid search + live reservation reads"]
  S6["6 Generate<br/>grounded draft · claim-level citations"]
  S7["7 Verify<br/>groundedness · commitments · PII · injection"]
  S8{"8 Gate<br/>deterministic · 15 conditions"}
  S9["9 Deliver<br/>disclosure · threading · hold delay"]
  S10["10 Observe<br/>log · audit sample · metrics · learning"]

  S1 --> S2 --> S3 --> S4 --> S5 --> S6 --> S7 --> S8
  S8 -->|auto_send| S9 --> S10
  S8 -->|human_review| Q["Agent queue<br/>M7"]
  S8 -->|abstain_and_escalate| Q
  Q --> S10

  S1 -. parse fail .-> QUAR["Quarantine + alert"]
  S2 -. spam / injection .-> Q
  S3 -. force human .-> Q
  S7 -. unsupported / contradiction .-> Q

  classDef model fill:#e8f0fe,stroke:#4285f4,color:#111;
  classDef code fill:#e6f4ea,stroke:#34a853,color:#111;
  class S3,S6,S7 model;
  class S2,S4,S8 code;
```

---

## 3. Autonomy gate (stage 8)

Auto-send requires **all 15 conditions (G01–G15) to pass**. Any single failure routes to a human, with
the routing depending on *which* condition failed. A kill switch or circuit breaker overrides everything.

```mermaid
flowchart TB
  D["Draft + verifier verdict + context"] --> G1

  G1{"G01-G03<br/>level · allowlist · risk ≤ max"}
  G2{"G04<br/>no hard-stop signal"}
  G3{"G05-G07<br/>confidence · groundedness · freshness"}
  G4{"G08-G09<br/>verification+DMARC · live read"}
  G5{"G10<br/>commitment guardrail"}
  G6{"G11<br/>language approved"}
  G7{"G12<br/>no human takeover · not excluded"}
  G8{"G13-G15<br/>rate/breaker · safety/tone · time window"}

  G1 -->|pass| G2 -->|pass| G3 -->|pass| G4 -->|pass| G5
  G5 -->|pass| G6 -->|pass| G7 -->|pass| G8
  G8 -->|all pass| AUTO["auto_send"]

  G1 -->|fail| QUEUE["Normal queue"]
  G7 -->|fail| QUEUE
  G2 -->|fail| SPEC["Specialist / senior queue"]
  G3 -->|"fail — reason shown"| REVIEW["Human review + failure reason"]
  G5 -->|"fail — reason shown"| REVIEW
  G4 -->|fail| REVIEW
  G6 -->|fail| REVIEW
  G8 -->|fail| HOLD["Review / hold"]

  KILL["Kill switch / circuit breaker"] -. overrides .-> G1

  classDef ok fill:#e6f4ea,stroke:#34a853,color:#111;
  classDef stop fill:#fce8e6,stroke:#ea4335,color:#111;
  class AUTO ok;
  class QUEUE,SPEC,REVIEW,HOLD,KILL stop;
```

---

## 4. Trust ladder

Autonomy is an explicit per-(tenant, brand, intent) state. **Promotion is human-gated** on measured
criteria — the product proposes, the supervisor disposes. **Demotion is automatic**: the circuit breaker
drops a level from anywhere the moment quality metrics slip.

```mermaid
stateDiagram-v2
  [*] --> L0
  L0: L0 Shadow — draft only, never send
  L1: L1 Assisted — every send human-approved
  L2: L2 Narrow auto — allowlisted R0, 100% audit
  L3: L3 Broad auto — expanded intents, sampled audit
  L4: L4 Autonomous — with exceptions

  L0 --> L1: supervisor promote
  L1 --> L2: promote, criteria met
  L2 --> L3: promote, R1 + integration
  L3 --> L4: promote

  L4 --> L3: circuit breaker
  L3 --> L2: circuit breaker
  L2 --> L1: circuit breaker
  L1 --> L0: manual off / kill switch

  note right of L2
    Promotion: human-gated on measured criteria
    Demotion: automatic, drops one level, any level
    Per tenant, per brand, per intent
  end note
```

---

## 5. Core data model

The shared entities (§10). Every entity carries a `tenant_id` and is isolated at the data layer
([ADR-0015](adr/0015-data-layer-tenant-isolation.md)). `Booking` is a short-TTL cached projection, never
the source of truth; per-customer data never enters the shared knowledge index.

```mermaid
erDiagram
  TENANT ||--o{ BRAND : has
  TENANT ||--o{ CUSTOMER : has
  TENANT ||--o{ AUTONOMYPOLICY : configures
  TENANT ||--o{ KNOWLEDGESOURCE : owns
  TENANT ||--o{ EVENT : runs
  TENANT ||--o{ EVALUATIONCASE : owns
  TENANT ||--o{ AUDITRECORD : records
  BRAND ||--o{ MAILBOX : has
  BRAND ||--o{ CONVERSATION : scopes
  CUSTOMER ||--o{ CONVERSATION : opens
  CONVERSATION ||--o{ MESSAGE : contains
  CONVERSATION }o--o| BOOKING : about
  CONVERSATION ||--o{ DRAFT : produces
  MESSAGE ||--o{ ATTACHMENT : has
  MESSAGE ||--|| UNDERSTANDING : yields
  DRAFT ||--o{ CITATION : cites
  DRAFT ||--|| GATEEVALUATION : evaluated_by
  DRAFT ||--o{ REVIEWACTION : edited_by
  DRAFT ||--o| SENTMESSAGE : sent_as
  KNOWLEDGESOURCE ||--o{ KNOWLEDGEITEM : yields
  CITATION }o--o| KNOWLEDGEITEM : resolves_to
  EVENT }o--o{ CONVERSATION : clusters

  TENANT {
    uuid id
    string region
    string autonomy_level
    json retention_policy
  }
  CONVERSATION {
    uuid id
    uuid tenant_id
    string status
    string risk_class
    string autonomy_outcome
  }
  GATEEVALUATION {
    uuid id
    json condition_results
    string outcome
    datetime evaluated_at
  }
  BOOKING {
    string external_id
    string status
    bool is_cached_projection
  }
```

---

## Regenerating

The `.mmd` sources are the editable form. To re-validate or re-export after an edit (no local `mmdc`
required — uses the Kroki API):

```bash
cd docs/diagrams
for f in *.mmd; do
  curl -s -X POST -H "Content-Type: text/plain" --data-binary "@$f" \
    https://kroki.io/mermaid/svg -o "${f%.mmd}.svg" && echo "rendered $f"
done
```
