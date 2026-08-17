# AI Email Support Desk for Tour Operators
## Product Requirements Document — v0.1 (draft for review)

| | |
|---|---|
| **Working product name** | TourDesk AI *(placeholder — see OD-01)* |
| **Document status** | Draft for review. Nothing here is agreed until marked so. |
| **Date** | 14 August 2026 |
| **Author** | Drafted with Claude, for GBR |
| **Audience** | Founder/product owner, prospective dev team, first design partners |
| **Product type** | Multi-tenant B2B SaaS sold to tour operators and travel agencies |

### How to review this document

1. Read **§3 (Review of your original brief)** first — it is the critique you asked for, and it shows what your six bullet points turn into once they meet reality.
2. Skim the requirement tables in **§7**. Every requirement has an ID and a priority. Mark any you disagree with by ID.
3. Answer the numbered questions in **§17 (Open decisions)**. Those are the gaps I cannot close for you. Answering them turns v0.1 into v1.0.
4. Ignore effort/roadmap numbers in §15 for now — they are indicative until a team is sized.

**Priority key:** `M` = Must (v1 cannot ship without it) · `S` = Should · `C` = Could · `W` = Won't, this release.

---

## 1. Executive summary

Tour operators receive a high volume of repetitive customer email. A large share of it is answerable from information the operator already publishes (destination guides, excursion catalogues, hotel factsheets, terms) or already holds in its reservation system (booking status, flight times, documents). Today that email is handled by agents who copy-paste, look things up in three systems, and answer slowly during exactly the weeks when volume peaks.

This product connects to an operator's support mailbox, understands each incoming email, retrieves grounded facts from the operator's own content and reservation system, and either **sends an answer automatically** (when it is provably safe to do so) or **hands a drafted answer to a human** in a purpose-built review console. Everything that happens is logged, searchable, filterable and measurable, and the system gets better over time by turning human corrections into reusable knowledge.

Three things make it sellable rather than merely useful:

- **Earned autonomy.** No operator will let an AI email their customers on day one. The product ships with a formal trust ladder: shadow mode → assisted → narrow auto-send → broad auto-send, with progression gated on measured accuracy, not on the vendor's promises. This is the single most important commercial feature.
- **Grounding and refusal.** The system answers only from cited sources and abstains otherwise. It is structurally forbidden from inventing prices, availability, or commitments — the class of error that creates legal liability for a package organiser.
- **Portability.** A connector framework means the same product sells to operators running different reservation back-ends, with a documented minimum integration contract.

**The economic promise to the buyer:** reduce cost per contact and first-response time, absorb seasonal peaks without seasonal hiring, and convert pre-sales enquiries faster. **The risk to manage:** one confidently wrong answer about a departure time or a refund destroys the trust that took months to build.

---

## 2. Problem, opportunity and buyer

### 2.1 The operator's problem

| Pain | What it looks like | What it costs |
|---|---|---|
| Repetitive volume | 40–70% of inbound email is a small number of recurring question types | Agent hours on work that creates no differentiation |
| Seasonality | Volume multiplies in booking season and collapses off-season; disruption events (strike, weather, airline failure) produce instant floods | Over-hiring, or queue collapse and reputational damage |
| Slow first response | Answers require looking across reservation system, website, supplier documents, inbox history | Lost pre-sales enquiries; complaints escalate while waiting |
| Knowledge locked in people | The best answers live in senior agents' heads and their sent folder | Long onboarding for new agents; inconsistent answers |
| Multilingual demand | Operators sell into several source markets; agents cover a subset of languages | Delays and stilted answers in minority languages |
| Compliance load | Complaint handling, information duties and response deadlines are tightening | Manual tracking, audit exposure |

### 2.2 Why now

- Retrieval-grounded LLMs are now good enough to draft accurate, on-brand answers **when they are given the right source material and forbidden to guess** — the second half of that sentence is the engineering problem this product solves.
- The revised EU Package Travel Directive, adopted by the Council on 30 March 2026, requires organisers to put **complaint-handling arrangements** in place before, during and after travel, and tightens information duties and refund timelines. Operators will need documented, deadline-tracked complaint workflows. A support platform that produces this evidence as a by-product has a compliance sales angle, not just an efficiency one.
- EU AI Act transparency obligations (Article 50) have applied since **2 August 2026**. Any operator deploying an AI that emails customers now has disclosure duties. Shipping compliance in the box removes a purchase blocker rather than creating one.

### 2.3 Who buys, who uses, who blocks

| Role | Cares about | Their objection |
|---|---|---|
| **Economic buyer** — COO / Head of Customer Service / owner | Cost per contact, headcount, peak survivability, response times | "What happens when it gets something wrong?" |
| **Champion** — Customer service manager | Queue control, agent productivity, consistency, reporting | "Will my team lose control of the inbox?" |
| **User** — Support agent | Fewer boring emails, faster handling, not being blamed for AI errors | "Is this replacing me?" |
| **Blocker** — IT / DPO / legal | Data protection, integration effort, security, AI Act, auditability | "Where does customer data go, and who trains on it?" |
| **Blocker** — Product/contracting team | Binding statements made to customers, price and availability accuracy | "If the AI promises something, do we have to honour it?" |

Requirements throughout this document are written to answer those five objections explicitly.

---

## 3. Review of your original brief

This is the critique you asked for. Your brief was a good product instinct; it is roughly 30% of a specification. Below: what you said, what it implies, and what it omits.

### 3.1 What you specified, restated as testable intent

| # | Your requirement | Reading |
|---|---|---|
| B1 | "Check emails on a specific email address" | Mailbox connectivity and polling |
| B2 | "Using AI directly respond if really positive it has the right answer" | Autonomous send gated on confidence |
| B3 | "Otherwise a human checks a proposed answer, edits it, sends it" | Human-in-the-loop review console |
| B4 | "Interface shows all requests and answers, able to filter" | Case list, search and filtering |
| B5 | "The model learns from previous answers" | Continuous improvement loop |
| B6 | "Information taken from agency website or a location the agency provides" | Knowledge ingestion from operator sources |

### 3.2 The eight gaps that matter most

**G1 — Your example questions are not one problem; they are four, with different risk.**
"What can I do in Antalya" is a public-content question. "When does my plane leave" is a personal-data question that requires identifying the customer and reading a system of record. "Can I change my hotel" is a **commercial commitment** — answering it wrongly creates an obligation. "I have not received my ticket reservation" is a document-delivery workflow, not a question at all. A single confidence threshold cannot govern all four. The system needs an intent taxonomy with per-intent risk classes and per-intent autonomy. *(→ §7 M3, M6)*

**G2 — Nothing in the brief identifies the customer, and most of your examples require it.**
Before the system can say anything about a booking it must answer: which booking is this, and is the person emailing entitled to that data? Sender address alone is weak evidence and is spoofable. Without an explicit identification-and-verification module, the product's most valuable use cases are also its largest GDPR exposure. *(→ §7 M2)*

**G3 — "Really positive it has the right answer" is not implementable as stated.**
Language models are poorly calibrated by default; a model's stated confidence is not a probability. Autonomy must be decided by a composite gate — retrieval quality, source freshness, an independent verification pass, intent risk, identity status, and tenant policy — not by asking the model whether it feels sure. *(→ §9)*

**G4 — "The model learns from previous answers" is the most dangerous line in the brief.**
Taken literally (fine-tuning on sent replies) it degrades silently, propagates any human mistake into every future answer, entrenches outdated facts, and creates a GDPR problem by baking customer personal data into weights. What you actually want is a **knowledge loop**: capture the human's edits, promote approved answers into a reviewed knowledge base, mine unanswered questions into a content backlog, and hold a frozen evaluation set so you can prove the system improved rather than assume it. *(→ §7 M8)*

**G5 — No concept of a conversation.**
Email is threads: replies to replies, follow-ups, forwards, three customers on one booking, out-of-office auto-responders. Without thread state you get duplicate answers, two agents replying to the same customer, and — the classic failure — an auto-reply loop where your system and a customer's holiday auto-responder email each other several thousand times overnight. *(→ §7 M1)*

**G6 — Nothing prevents the system from making promises.**
Under package travel law, information the organiser gives is binding. An AI that writes "yes, we can move you to the Meridian for no extra charge" has created an expectation the operator may have to honour. A hard guardrail is required: **no price, no availability, no confirmation of a change, no goodwill gesture may appear in any message unless it came from a system of record or a human approved it.** This is a non-negotiable product rule and a sales asset. *(→ §7 M5, FR-M5-06)*

**G7 — No handling of the emails you must never automate.**
Complaints, compensation claims, illness, death, accessibility needs, unaccompanied minors, legal threats, regulator or media contact, and data-subject requests must be detected and routed to a human — often a senior one — regardless of how confident the model is. In the EU these overlap with the revised Package Travel Directive's complaint-handling duties, which means the workflow needs deadlines and an audit trail, not just a flag. *(→ §7 M3, M13)*

**G8 — It is described as one inbox for one agency, but you want to sell it.**
That changes everything: tenant isolation, self-serve or guided onboarding, per-tenant brand and tone, per-tenant policy, per-tenant knowledge, usage metering and billing, role-based access, SSO for larger buyers, a connector framework instead of one hard-wired integration, and an ROI dashboard the buyer can show their own board. *(→ §7 M11, M12)*

### 3.3 Six things missing that are opportunities, not just gaps

| | Opportunity | Why it matters commercially |
|---|---|---|
| O1 | **Crisis mode** — detect a spike of emails on one topic (airline collapse, wildfire, strike), let a supervisor write one authoritative answer, personalise and apply it to the whole cluster, plus proactive outbound to affected customers | This is the day the buyer remembers. It is the strongest demo you can give and it is hard for generic helpdesk AI to do. |
| O2 | **Pre-sales as a first-class case type** — "what do you recommend for a youth trip" is revenue, not support | Lets you sell on revenue uplift, not only cost reduction. Track enquiry → booking conversion. |
| O3 | **Attachment re-delivery** — "I never got my tickets" resolved end-to-end by fetching and re-sending the document | One of the highest-volume, most automatable intents; a visible win in week one. |
| O4 | **Edit-distance telemetry** — measure how much humans change each draft | The honest quality metric, the input to the trust ladder, and a compelling number in a QBR. |
| O5 | **Multilingual answering with a reviewer's translation view** | Lets a Romanian-speaking team supervise Danish or Polish replies. Directly expands which markets the buyer can serve. |
| O6 | **Compliance evidence as output** — complaint register, response deadlines, AI disclosure logs | Turns a cost-centre purchase into a risk-reduction purchase. |

### 3.4 What I recommend cutting from v1

Ambition is the enemy of a first release. Explicit non-goals in §5.2 include write-back to the reservation system, voice/phone, and fine-tuned models. All three are tempting and all three will delay the release by months while adding the majority of the risk.

---

## 4. Product vision and design principles

> **Vision.** Every routine customer email a tour operator receives is answered correctly, in the customer's language, within minutes — and every email that should be answered by a person reaches the right person faster, with the work already done.

These principles resolve disputes when requirements conflict. They are ranked; higher beats lower.

1. **Never guess.** An unanswered email is recoverable. A confidently wrong one is not. When grounding is missing, the system abstains and escalates. Abstention is a success, not a failure.
2. **Never commit.** The system may describe policy; it may not create obligations. Prices, availability, changes, refunds and goodwill come from a system of record or a human.
3. **Trust is earned in stages.** Autonomy expands only on evidence, per tenant, per intent, and can be revoked instantly and automatically.
4. **The human is the editor, not the typist.** The console is optimised for a fast reviewer with a good draft, not for someone composing from scratch.
5. **Everything is traceable.** Every sent message can be reconstructed: what came in, what was retrieved, what the model produced, what the human changed, who approved it, when.
6. **The operator owns their voice and their data.** Tone, policy, knowledge and customer data belong to the tenant, are isolated per tenant, and are never used to train shared models.
7. **Degrade, don't fail.** No reservation system? Still answer content questions. No knowledge base? Still triage and route. An outage means a queue for humans, never a wrong answer.

---

## 5. Scope

### 5.1 In scope for v1

- Email intake from a tenant's support mailbox(es), multiple mailboxes and brands per tenant.
- Language detection and answering in the customer's language.
- Intent, entity, sentiment and risk classification against a travel-specific taxonomy.
- Knowledge ingestion from website crawl, uploaded documents and structured feeds; retrieval-grounded answer generation with citations.
- Read-only reservation-system integration via a connector framework, with a reference connector for the first design partner's back-end.
- Customer identification and verification against booking data.
- Composite autonomy gate with per-intent, per-tenant policy and a trust ladder.
- Agent review console: prioritised queue, draft review with sources, editing, sending, assignment, internal notes, SLA timers, snooze and escalation.
- Case list with full-text search and filtering; case detail with complete audit trail.
- Knowledge loop: edit capture, approved-answer promotion, knowledge-gap backlog, frozen evaluation set with regression reporting.
- Analytics: automation rate, response times, edit distance, audit accuracy, volume by intent, ROI view.
- Crisis/mass-event detection and bulk-answer workflow.
- Multi-tenant administration, RBAC, per-tenant configuration, usage metering.
- AI Act disclosure, GDPR tooling (retention, erasure, export), audit logging.

### 5.2 Explicitly out of scope for v1 *(with the reason, so it can be revisited deliberately)*

| Not in v1 | Why | Revisit |
|---|---|---|
| Writing to the reservation system (making changes, cancellations, rebooking) | Multiplies integration surface, liability and testing; needs per-operator process design | v2 |
| Live chat widget, WhatsApp, social DMs | Different latency and UX contract; email first proves the core | v2 |
| Voice / phone / call transcription | Entirely different pipeline | v3+ |
| Fine-tuned or self-hosted models | Retrieval + prompting + few-shot reaches most of the value; fine-tuning adds cost, lock-in and silent-drift risk | v3+, only with evidence |
| Outbound marketing campaigns | Different product, different compliance regime (consent) | Not planned |
| Payment collection in-thread | PCI scope | Not planned |
| Autonomous handling of complaints or compensation | Legal and reputational risk exceeds the benefit | Not planned as autonomous; assisted only |

### 5.3 Assumptions

- The operator can provide, or point at, reasonably accurate published content. *(If the website is wrong, the answers are wrong. See RSK-03.)*
- The operator's reservation system exposes some read API, database view, or scheduled export. *(If not, booking-specific intents stay human — see the degraded mode in §7 M12.)*
- The operator is willing to run in shadow mode for an agreed period before any auto-send.
- Initial target market is EU/EEA operators; EU data residency and EU regulation are the baseline.

---
## 6. Users, roles and top user stories

### 6.1 Roles (RBAC)

| Role | Scope | Key permissions |
|---|---|---|
| **Agent** | Own tenant, assigned queues | View and answer cases, edit drafts, send, snooze, escalate, add notes |
| **Senior agent / Team lead** | Own tenant | All agent rights + reassign, handle R3 sensitive cases, approve knowledge promotions, bulk actions |
| **Supervisor / CS manager** | Own tenant | All above + configure autonomy policy, run crisis mode, view all analytics, manage users |
| **Content owner** | Own tenant | Manage knowledge sources, resolve knowledge gaps, approve/retire knowledge items, no case access required |
| **Tenant admin** | Own tenant | Mailboxes, brands, integrations, retention settings, SSO, billing view |
| **Auditor (read-only)** | Own tenant | Read all cases, audit trails and reports; no send rights |
| **Vendor operator (you)** | Cross-tenant, restricted | Provisioning, health, support access **with tenant-approved, time-boxed, logged elevation only** |

### 6.2 Primary user stories

**Agent**

- As an agent, I open the queue and see the most urgent case first, already classified, with a draft answer and its sources, so I can approve or fix it in under a minute.
- As an agent, I can see the customer's booking summary next to the email so I do not switch systems.
- As an agent, I can tell at a glance which sentences of the draft are supported by which source.
- As an agent, when I edit a draft, I can flag *why* (wrong fact / wrong tone / missing info) in one click, so the system learns.
- As an agent, I cannot accidentally answer a case another agent already opened.
- As an agent handling a Danish email I do not read, I can see a translation alongside the original and the draft.

**Supervisor**

- As a supervisor, I can see automation rate, accuracy and how much my team edits drafts, per intent, over time.
- As a supervisor, I can turn auto-send off entirely, or for one intent, in a single click, and it takes effect immediately.
- As a supervisor, during a disruption I can see that 340 emails are about the same cancelled flight, write one answer, and release it to all of them after review.
- As a supervisor, I get alerted when the edit rate or escalation rate jumps, before customers complain.

**Content owner**

- As a content owner, I see the questions the system could not answer last week, grouped by theme and ranked by volume, so I know what to write.
- As a content owner, I can add a canonical answer and see it used, and measure whether it reduced escalations.
- As a content owner, I get warned when a source page has changed or has not been reviewed within its freshness window.

**Buyer / admin**

- As a CS manager evaluating the product, I can run it in shadow mode against my real inbox for four weeks and see exactly what it *would* have sent, with no risk.
- As a DPO, I can produce, for any customer, everything the system holds about them, and delete it.
- As a tenant admin, I can prove to my auditor which messages were AI-sent, with what disclosure, based on which sources.

---

## 7. Functional requirements

Requirements are grouped by module. IDs are stable; do not renumber when editing — mark superseded ones instead.

### M1 — Mail connectivity and conversation management

| ID | Requirement | Pri |
|---|---|---|
| FR-M1-01 | Connect to a tenant mailbox over **IMAP/SMTP**, **Microsoft Graph** or **Gmail API**, behind a single internal mail-provider interface so providers are interchangeable. | M |
| FR-M1-02 | Support multiple mailboxes and multiple sending identities (brands, domains, source markets) per tenant, each with its own configuration, knowledge scope and policy. | M |
| FR-M1-03 | Poll or subscribe for new mail; a new message must enter the pipeline within **60 seconds** of arrival. | M |
| FR-M1-04 | Parse MIME correctly: plain text and HTML bodies, inline images, quoted history, forwarded chains, non-UTF-8 charsets, and malformed messages. Never lose a message due to a parse failure — quarantine and alert instead. | M |
| FR-M1-05 | Thread messages into **Conversations** using `Message-ID`, `In-Reply-To`, `References`, plus a fallback heuristic (subject normalisation + participants + time window) when headers are absent. | M |
| FR-M1-06 | Detect and suppress **auto-responders and loops**: honour `Auto-Submitted`, `X-Auto-Response-Suppress`, `Precedence: bulk/list`, vacation replies, bounces and delivery-status notifications. Enforce a hard per-address reply cap (default: max 3 automated messages per address per 24h, and never reply to an address that has replied automatically twice). | M |
| FR-M1-07 | Detect and classify **bounces (hard/soft)** and mark the case as undeliverable rather than silently succeeding. | M |
| FR-M1-08 | Verify inbound authentication (**SPF / DKIM / DMARC**) and record the result. A failed or absent result blocks identity verification (see M2) and blocks auto-send of any personal data. | M |
| FR-M1-09 | Handle attachments: store securely, scan for malware, extract text from PDF/DOCX/images (OCR) for context, detect and mask sensitive PII in prompts (passport and card numbers). | M |
| FR-M1-10 | Send replies that thread correctly in the customer's client (`In-Reply-To`, `References`, preserved subject), from the tenant's domain, with the tenant's signature and formatting. | M |
| FR-M1-11 | Document the **deliverability requirements** the tenant must meet (SPF, DKIM, DMARC alignment for the sending domain) and validate them at onboarding, blocking go-live until they pass. | M |
| FR-M1-12 | Deduplicate identical messages (same `Message-ID`, or same sender+subject+body hash within a window) — customers resend when anxious. | S |
| FR-M1-13 | Support a configurable **hold-before-send delay** (default 60s) during which an auto-send can be cancelled by a supervisor or by a newly arrived message in the same thread. | S |
| FR-M1-14 | Support CC/BCC participants, and detect when a booking has multiple correspondents (traveller, purchaser, agent). | S |
| FR-M1-15 | Import historical mail (sent + received, configurable window) at onboarding, for tone learning, evaluation-set construction and knowledge mining. | S |
| FR-M1-16 | Optional shared-mailbox coexistence: leave messages in place and mark with labels/categories so the tenant's existing client still works during transition. | C |

### M2 — Customer identification and verification

> This module is the difference between a demo and a product. Everything personal depends on it.

| ID | Requirement | Pri |
|---|---|---|
| FR-M2-01 | Extract candidate identifiers from the email: booking reference, invoice number, passenger names, travel dates, destination, phone. Handle common formatting errors and OCR from attached confirmations. | M |
| FR-M2-02 | Resolve the sender to zero, one or many **Bookings** via the reservation connector, using reference match, email match and fuzzy name+date match. | M |
| FR-M2-03 | Assign a **verification level** to every case: `unverified`, `weak` (sender address matches booking contact, DMARC pass), `strong` (weak + a second matching factor such as booking reference or exact travel dates), `human-verified` (an agent confirmed identity). | M |
| FR-M2-04 | Enforce a **disclosure policy matrix**: which data classes may be disclosed at which verification level, per tenant. Default: nothing personal below `weak`; documents and full itinerary require `strong`. | M |
| FR-M2-05 | When identity is ambiguous or insufficient, generate a compliant "please confirm your booking reference" reply rather than guessing, and never reveal whether a given email address has a booking. | M |
| FR-M2-06 | Never disclose data for a booking the sender is not a recorded contact on, even if the reference is correct — reference numbers get forwarded. | M |
| FR-M2-07 | Log every identity decision with the evidence used, for audit and incident investigation. | M |
| FR-M2-08 | Support a manual override where an agent links a case to a booking, recording who and why. | M |
| FR-M2-09 | Detect multiple bookings for one customer and ask which one the question concerns, rather than assuming the nearest departure. | S |

### M3 — Understanding: language, intent, entities, risk

| ID | Requirement | Pri |
|---|---|---|
| FR-M3-01 | Detect the customer's language per message (not per thread — people switch), with a per-tenant default fallback. Support at minimum the tenant's declared market languages. | M |
| FR-M3-02 | Classify each message against the **intent taxonomy** (§8), returning a ranked list with scores, supporting **multi-intent** messages (one email routinely contains three questions). | M |
| FR-M3-03 | Decompose multi-intent messages into answerable units; a single reply must address all of them, and the autonomy gate applies to the **riskiest** unit in the message. | M |
| FR-M3-04 | Extract entities: booking reference, destination, hotel, dates, passenger count and composition (adults/children/ages), flight number, product/package name, amounts. | M |
| FR-M3-05 | Assign a **risk class** R0–R4 (§8.2) derived from intent, entities and content signals — not from model confidence. | M |
| FR-M3-06 | Detect **hard-stop signals** that force human handling regardless of anything else: complaint, compensation or refund claim, legal or regulatory reference, media/press, illness/injury/death, accessibility or medical need, unaccompanied minor, safeguarding concern, abusive content, data-subject request, insolvency question, chargeback/dispute. | M |
| FR-M3-07 | Detect **prompt-injection and manipulation attempts** in body text and attachments (instructions addressed to the AI, attempts to elicit policy overrides or free upgrades) and force human review. Content from customer emails and from crawled pages must never be treated as instructions. | M |
| FR-M3-08 | Score sentiment and urgency; combine with departure proximity (a question 8 hours before departure outranks one about next summer). | M |
| FR-M3-09 | Detect out-of-scope mail: spam, newsletters, job applications, supplier invoices, B2B agent traffic — and route or file without a customer reply. | M |
| FR-M3-10 | Every classification must be overridable by an agent, and overrides feed the learning loop. | M |
| FR-M3-11 | Allow tenants to add custom intents and custom hard-stop keyword sets without a code release. | S |

### M4 — Knowledge platform

| ID | Requirement | Pri |
|---|---|---|
| FR-M4-01 | Ingest from a **website crawl**: scoped by domain and path rules, respecting `robots.txt`, with scheduled re-crawls and change detection. | M |
| FR-M4-02 | Ingest **uploaded documents**: PDF, DOCX, XLSX, PPTX, CSV, plain text, with layout-aware extraction (tables in hotel factsheets and excursion price lists must survive). | M |
| FR-M4-03 | Ingest **structured feeds**: product/excursion catalogues, hotel attribute data, departure schedules, FAQ exports — via CSV/JSON drop or API. Structured facts must be retrievable as facts, not as prose. | M |
| FR-M4-04 | Ingest **canonical answers** authored directly in the console by content owners (the most important source type — it is where the learning loop lands). | M |
| FR-M4-05 | Chunk, embed and index content with metadata: source, URL, language, brand, destination, product, season/validity window, last-verified date, owner, confidence tier. | M |
| FR-M4-06 | Support **hybrid retrieval** (semantic + keyword/BM25) with metadata filtering, because hotel names, flight numbers and product codes fail under pure semantic search. | M |
| FR-M4-07 | Enforce **source authority tiers**: canonical answers > structured feed > official policy document > website page > mined historical answer. Higher tier wins on conflict, and conflicts are surfaced to the content owner. | M |
| FR-M4-08 | Enforce **freshness**: every source has a review TTL (default by type; overridable). Content past TTL is flagged, down-weighted, and **excluded from auto-send grounding** while remaining available to human agents with a staleness warning. | M |
| FR-M4-09 | Detect **temporal validity**: seasonal content, departed/ended products, past-dated schedules must not be served as current. Content with an expiry date is withdrawn automatically. | M |
| FR-M4-10 | Multilingual knowledge: answer in the customer's language even when the source is in another; record which language the source was in; allow per-language overrides where translations exist and differ. | M |
| FR-M4-11 | Provide a knowledge browser: search, view chunks, see usage counts, see which answers cited an item, retire or edit an item, see the review queue. | M |
| FR-M4-12 | Scope every knowledge item to a tenant and optionally to a brand. Cross-tenant leakage of knowledge or customer data is a **P0 defect**. | M |
| FR-M4-13 | Never place per-customer booking data in the shared knowledge index. Personal facts come from the reservation connector at answer time and are not retained in the index. | M |
| FR-M4-14 | Support content exclusions: URL patterns and topics that must never be used for answers (e.g. legal terms pages the operator wants quoted verbatim only, or campaign pages with expired prices). | S |
| FR-M4-15 | Mine historical sent mail into candidate knowledge items, presented to a content owner for approval — never auto-published. | S |

### M5 — Answer generation

| ID | Requirement | Pri |
|---|---|---|
| FR-M5-01 | Generate answers **only** from retrieved sources and system-of-record data supplied in context. No unsourced factual claims. | M |
| FR-M5-02 | Attach **citations at sentence or claim level**, resolvable to the exact source chunk and URL/document page, and displayed to the agent. | M |
| FR-M5-03 | If grounding is insufficient for any part of the question, the draft must say so explicitly and the case must be escalated — partial answers are allowed only when the unanswered part is clearly marked for the agent. | M |
| FR-M5-04 | Apply per-tenant **voice and format**: greeting and sign-off conventions, formality (including T/V distinction in languages that have it), signature, brand names, banned words, reading level, plain-text vs HTML. | M |
| FR-M5-05 | Answer in the customer's language, at native quality; if the tenant has no approved capability in that language, draft-only, never auto-send. | M |
| FR-M5-06 | **Commitment guardrail.** A generated message may not contain a price, an availability statement, a fee waiver, a confirmation of a change or cancellation, a compensation offer, or any new obligation, unless that exact value came from the reservation connector or from a human. Enforced by a deterministic post-generation check, not by prompt instruction alone. | M |
| FR-M5-07 | **Verification pass.** An independent model call checks the draft against the retrieved sources and returns per-claim support, plus flags for unsupported claims, contradiction, commitments and PII leakage. Its verdict is an input to the autonomy gate. | M |
| FR-M5-08 | Never fabricate links, phone numbers, addresses, document names or reference numbers; all such values must be resolved from configuration or sources. | M |
| FR-M5-09 | Include the tenant's configured **AI disclosure** in messages produced by the system, per §13.2. | M |
| FR-M5-10 | Compose personalised answers that merge public knowledge with booking facts ("Your flight to Antalya departs at 06:15 on 14 July; check-in opens 3 hours before"). | M |
| FR-M5-11 | Attach documents from the reservation system when the intent requires it (tickets, vouchers, invoices), subject to verification level. | M |
| FR-M5-12 | Offer relevant, truthful **next steps and cross-sell** where configured — excursions available at the customer's destination and dates, transfers, luggage — sourced from the catalogue, never invented, and disabled by default. | S |
| FR-M5-13 | Generate 2–3 alternative draft variants on request (shorter, warmer, firmer) for the agent to pick from. | C |
| FR-M5-14 | Suggest an internal note for the agent (what to check, what is uncertain) separate from the customer-facing text. | S |

### M6 — Autonomy and the confidence gate

> The commercial heart of the product. See §9 for the model detail.

| ID | Requirement | Pri |
|---|---|---|
| FR-M6-01 | Implement the **trust ladder** as an explicit per-tenant state: `L0 Shadow` (draft, never send, compare against what the human sent) → `L1 Assisted` (every send human-approved) → `L2 Narrow auto` (allowlisted low-risk intents auto-send, 100% post-send audit) → `L3 Broad auto` (expanded intents, sampled audit) → `L4 Autonomous with exceptions`. | M |
| FR-M6-02 | Auto-send requires **all** gate conditions to pass (§9.2). Any single failure means human review. Conditions are evaluated deterministically and the result is logged with per-condition detail. | M |
| FR-M6-03 | Autonomy policy is configurable **per tenant, per brand and per intent**, including a per-intent confidence threshold and a per-intent maximum risk class. | M |
| FR-M6-04 | Provide a **global kill switch** and per-intent switches that take effect within seconds, including for messages already drafted but not yet sent. | M |
| FR-M6-05 | Implement an automatic **circuit breaker**: if edit rate, negative-feedback rate, escalation rate or audit failure rate for an intent exceeds thresholds over a rolling window, autonomy for that intent drops one level automatically and alerts the supervisor. | M |
| FR-M6-06 | Rate-limit auto-sends per tenant per hour and per recipient, with a hard daily ceiling, to bound the blast radius of any failure. | M |
| FR-M6-07 | Never auto-send in a thread where a human has already replied, unless a supervisor explicitly returns the thread to automation. | M |
| FR-M6-08 | Never auto-send to a customer who has requested human handling, or who is on a tenant's exclusion list (VIPs, complainants in progress, B2B partners, known-vulnerable customers). | M |
| FR-M6-09 | Support **time-window rules**: e.g. auto-send permitted only within business hours, or only outside them, per tenant. | S |
| FR-M6-10 | Promotion between trust levels requires an explicit supervisor action, gated on the system showing that the measured criteria are met — the product proposes, the human disposes. | M |
| FR-M6-11 | Auto-sent messages must invite correction ("if this does not answer your question, reply and a colleague will take over"), and a reply to an auto-sent message escalates to a human by default. | M |

### M7 — Agent console

| ID | Requirement | Pri |
|---|---|---|
| FR-M7-01 | **Prioritised work queue** ordered by a configurable score combining urgency, departure proximity, SLA remaining, risk class, sentiment and age. | M |
| FR-M7-02 | **Claim/lock**: opening a case assigns it to that agent with a visible lock and an idle-release timeout. Two agents must never answer the same customer. | M |
| FR-M7-03 | **Review view** in three panes: the customer's message (with thread history), the draft (editable, rich text), and the evidence panel (cited sources, booking summary, customer history). | M |
| FR-M7-04 | Inline citation display: hovering or selecting a claim highlights its source; unsupported sentences are visually marked. | M |
| FR-M7-05 | **One-keystroke actions**: approve & send, edit & send, reject & rewrite, escalate, snooze, reassign, mark spam, request info. Full keyboard operation — agents are fast and the mouse is the bottleneck. | M |
| FR-M7-06 | **Structured feedback on edit**: when an agent changes a draft, capture a reason code (wrong fact / missing info / wrong tone / wrong language / policy issue / customer-specific nuance / other) with optional comment. Reason capture must take one click and be skippable to avoid it being gamed. | M |
| FR-M7-07 | **Booking panel**: itinerary, flights, hotel, pax, payment status, documents issued, change/cancel policy, prior contacts — read-only, from the connector. | M |
| FR-M7-08 | **Translation view**: original message, machine translation, draft, and back-translation of the draft, so a reviewer can supervise a language they do not speak. Clearly label machine translation. | M |
| FR-M7-09 | **Internal notes** and @mentions, never visible to the customer, with a hard visual distinction between internal and outbound text. | M |
| FR-M7-10 | **Escalation** to a named person, team or queue with a reason, preserving context. | M |
| FR-M7-11 | **Snippets/canned responses**, per tenant and personal, insertable by shortcut. | S |
| FR-M7-12 | **SLA timers** visible per case with breach warnings; SLA definitions configurable per tenant, per intent, per channel. | M |
| FR-M7-13 | **Case list** with saved views and filters (see FR-M7-14) and full-text search across message bodies, customer, booking reference, destination and draft content. | M |
| FR-M7-14 | Filter dimensions must include: status, intent, risk class, autonomy outcome (auto-sent / drafted / escalated / abstained), confidence band, language, brand/mailbox, assigned agent, SLA state, sentiment, destination, departure window, date range, edited-vs-unedited, audit result, tags. Filters must be combinable and shareable as a saved view. | M |
| FR-M7-15 | **Case detail / audit trail**: full chronological record — inbound message, classification, identity decision, retrieved sources, model versions and prompts used, draft, gate evaluation with per-condition results, human edits with diff, send record, customer reply. Exportable. | M |
| FR-M7-16 | **Undo/recall window** for auto-sent messages within the hold delay, and a documented "we cannot recall a delivered email" behaviour after it. | S |
| FR-M7-17 | Bulk actions on a filtered set: assign, tag, close, apply a reviewed cluster answer (see M9). | S |
| FR-M7-18 | Accessibility: WCAG 2.2 AA for the console. | S |
| FR-M7-19 | Agent-visible indicator of the current autonomy level and whether this case was eligible for auto-send and why not. Transparency to the team prevents the "the robot is doing something weird" anxiety that kills adoption. | S |

### M8 — Learning and improvement loop

> Replaces "the model learns from previous answers" with something that measurably improves and cannot silently regress.

| ID | Requirement | Pri |
|---|---|---|
| FR-M8-01 | **Capture the delta** between every generated draft and the message actually sent, storing a structured diff, an edit-distance metric, and the agent's reason code. | M |
| FR-M8-02 | **Knowledge-gap mining**: cluster cases that abstained, scored low confidence, or were heavily edited, by semantic similarity; rank clusters by volume × cost; present them to content owners as a prioritised backlog with example emails. | M |
| FR-M8-03 | **Canonical answer promotion**: an approved reply can be promoted to a canonical knowledge item in one click, with the sensitive/personal parts stripped, subject to content-owner approval. Nothing enters the knowledge base without a human approving it. | M |
| FR-M8-04 | **Tone example bank**: maintain a per-tenant set of exemplary approved replies used as few-shot examples, refreshed as style evolves, with size and recency limits. | M |
| FR-M8-05 | **Frozen evaluation set**: a per-tenant, versioned set of representative cases with agreed correct answers, held out from all improvement work. Every prompt, model, retrieval or knowledge change is scored against it before rollout. | M |
| FR-M8-06 | **Regression gate**: no configuration or model change reaches production if evaluation-set accuracy, groundedness or safety scores drop below the current baseline. | M |
| FR-M8-07 | **Post-send audit sampling**: sample auto-sent messages (100% at L2, configurable at L3+) for human accuracy rating; results feed the circuit breaker and the analytics. | M |
| FR-M8-08 | **Customer-signal feedback**: treat a customer reply to an auto-sent answer, a repeat question, or an escalation as a negative signal; treat thread closure without follow-up as a weak positive. Optionally add a one-click satisfaction link in replies. | S |
| FR-M8-09 | **Contradiction detection**: when a promoted answer conflicts with existing knowledge, block promotion and route to the content owner. | S |
| FR-M8-10 | **Change log and rollback**: every knowledge, prompt and policy change is versioned, attributed, and revertible. | M |
| FR-M8-11 | Learning must be **tenant-isolated by default**. Cross-tenant learning, if ever offered, requires explicit opt-in and must be limited to non-identifying, non-competitive artefacts. Customer personal data is never used for model training. | M |
| FR-M8-12 | Explicitly **no fine-tuning in v1**. If added later it must be per-tenant, opt-in, evaluated against the frozen set, and must not become the mechanism for factual knowledge. | M |

### M9 — Crisis / mass-event mode

| ID | Requirement | Pri |
|---|---|---|
| FR-M9-01 | Detect volume anomalies overall and per topic/destination against a seasonal baseline, and alert supervisors within minutes. | M |
| FR-M9-02 | Cluster the surge by semantic topic and affected booking attributes (destination, departure date, flight, hotel, supplier). | M |
| FR-M9-03 | **Event workspace**: create an Event, attach clusters and affected bookings, and record the operator's official position on it (a single authored statement, versioned). | M |
| FR-M9-04 | **Cluster answer**: a supervisor writes/approves one answer; the system personalises it per customer using booking data and language, produces per-case drafts, and releases them for bulk approval or (if policy allows) auto-send. | M |
| FR-M9-05 | **Automation freeze** on the affected topic until the official position exists — during a disruption the knowledge base is, by definition, out of date. This is a safety default, not an option. | M |
| FR-M9-06 | **Proactive outbound** to affected customers who have not yet written in, subject to explicit supervisor approval per send. | S |
| FR-M9-07 | Event reporting: how many customers affected, contacted, resolved, still open — the report the operator's management will ask for that evening. | S |

### M10 — Analytics, reporting and ROI

| ID | Requirement | Pri |
|---|---|---|
| FR-M10-01 | Operational dashboard: inbound volume, backlog, first-response time, resolution time, SLA compliance, by queue and agent. | M |
| FR-M10-02 | Automation dashboard: automation rate (auto-sent ÷ total answerable), assist rate, abstention rate, by intent and over time. | M |
| FR-M10-03 | Quality dashboard: median and distribution of edit distance, edit reason codes, audit accuracy, circuit-breaker events, customer follow-up rate on auto-sent answers. | M |
| FR-M10-04 | Knowledge dashboard: coverage, top knowledge gaps, stale sources, most-cited items, items never cited. | M |
| FR-M10-05 | **ROI view**: handling-time saved, cost per contact before/after, peak absorbed, expressed in the tenant's currency using their configured agent cost assumptions. Must be exportable for a board pack. | M |
| FR-M10-06 | Pre-sales view: enquiry volume by destination/theme, response time, and — where the tenant can supply the outcome — enquiry-to-booking conversion. | S |
| FR-M10-07 | Compliance reports: complaint register with deadlines and status, AI-disclosure log, data-request log, autonomy policy change history. | M |
| FR-M10-08 | Scheduled email/PDF reports and CSV export; read-only API for the tenant's own BI. | S |

### M11 — Tenancy, administration and onboarding

| ID | Requirement | Pri |
|---|---|---|
| FR-M11-01 | Hard tenant isolation of all data, knowledge, configuration and analytics, enforced at the data layer (not only in application code). | M |
| FR-M11-02 | Guided onboarding wizard: connect mailbox → verify sending domain → add knowledge sources → configure brand voice → connect reservation system (or skip) → set languages and SLAs → import history → start shadow mode. | M |
| FR-M11-03 | Per-tenant configuration of: brands, mailboxes, languages, tone, signatures, business hours, SLAs, intents, autonomy policy, disclosure text, retention period, exclusion lists. | M |
| FR-M11-04 | User management with RBAC (§6.1); SSO/SAML/OIDC and SCIM provisioning for larger tenants. | M (SSO) / S (SCIM) |
| FR-M11-05 | Usage metering per tenant: conversations, messages, auto-sends, tokens, storage — for billing and for internal unit-economics tracking. | M |
| FR-M11-06 | Tenant-facing status and health: connector state, mailbox state, crawl freshness, last error, with alerting. | M |
| FR-M11-07 | Sandbox/test mode: replay real or synthetic emails without sending anything, for tenant training and for regression testing. | M |
| FR-M11-08 | Vendor support access requires tenant-granted, time-boxed, purpose-logged elevation. | M |
| FR-M11-09 | Self-serve trial path: connect a mailbox, run shadow mode, see a report — without a project. This is what makes the product scale beyond hand-held deployments. | S |

### M12 — Integrations and connector framework

| ID | Requirement | Pri |
|---|---|---|
| FR-M12-01 | Define a **Reservation Connector Interface** — the minimum contract any back-end must satisfy — and implement all booking-dependent features against it only. Read-only in v1. | M |
| FR-M12-02 | The interface must provide: `findBookingsByReference`, `findBookingsByEmail`, `findBookingsByNameAndDates`, `getBooking` (status, pax and ages, dates, destination, accommodation, transport, payment status, balance due date), `getItinerary`, `getFlightSchedule`, `getDocuments` + `fetchDocument`, `getChangeAndCancellationPolicy`, `getContactsOnBooking`. | M |
| FR-M12-03 | Ship a **reference connector** for the first design partner's system, plus a **generic connector** over CSV/SFTP or database view for operators without an API, and a **file-drop connector** for scheduled exports. | M |
| FR-M12-04 | **Degraded mode**: when no reservation data is available, booking-dependent intents are classified, triaged and routed to humans with context, and content-only intents still automate. The product must be sellable at reduced value on day one without integration. | M |
| FR-M12-05 | Cache booking data with a short TTL and always re-read before any auto-send of a time-critical fact (departure times change). | M |
| FR-M12-06 | Helpdesk interoperability: optionally create/update tickets in Zendesk/Freshdesk/HubSpot, or run standalone. | S |
| FR-M12-07 | Flight status data source for departure changes and disruption detection. | S |
| FR-M12-08 | Webhooks and an outbound API so tenants can trigger their own workflows on case events. | S |
| FR-M12-09 | Connector conformance test suite so a new back-end can be certified without reading the product's source. | S |
| FR-M12-10 | Write-back operations (change requests, notes, tasks) — **v2**, and behind explicit per-operation permission. | W |

### M13 — Compliance, trust and safety

| ID | Requirement | Pri |
|---|---|---|
| FR-M13-01 | **AI disclosure** (EU AI Act Art. 50): configurable, tenant-visible disclosure text included in AI-generated customer messages; cannot be disabled below the legal minimum for tenants in scope; logged per message. See §13.2. | M |
| FR-M13-02 | **Machine-readable marking** of AI-generated message content where required, and a record of which model and version produced each message. | M |
| FR-M13-03 | **Complaint workflow** aligned to the revised Package Travel Directive: complaints are detected, registered with a timestamp, assigned an owner and a response deadline, tracked to closure, and reportable. Never auto-answered. | M |
| FR-M13-04 | **Data-subject requests** (access, erasure, rectification, portability, objection) are detected as an intent, routed to a designated handler, and supported by tooling that can export and delete everything the platform holds about a person across cases, attachments, indexes and logs. | M |
| FR-M13-05 | **Retention policy** per tenant per data class, with automated deletion and a documented default (see §11.4). | M |
| FR-M13-06 | **PII minimisation** in model calls: mask card numbers, passport numbers, national IDs and health data before they reach any model; document exactly what is sent to which sub-processor. | M |
| FR-M13-07 | **No training on tenant data** by the model provider — contractually and technically enforced; documented in the DPA and surfaced in the product's trust page. | M |
| FR-M13-08 | **EU data residency** for storage and processing, including model inference, for EU tenants. | M |
| FR-M13-09 | Maintain a **sub-processor list** and notify tenants of changes. | M |
| FR-M13-10 | Immutable audit log of security-relevant and send-relevant events, retained independently of case deletion where lawful. | M |
| FR-M13-11 | Human-oversight evidence: the product must be able to demonstrate, per message, who or what authorised the send and on what basis. | M |
| FR-M13-12 | Documented incident process for a wrong or unauthorised send, including tenant notification, blast-radius report and corrective action. | M |
| FR-M13-13 | Accessibility, safeguarding and vulnerable-customer routing: configurable signals that force senior human handling. | S |
| FR-M13-14 | Roadmap to ISO 27001 / SOC 2, because enterprise buyers will ask in the security questionnaire. | S |

---

## 8. Domain model: intents and risk

### 8.1 Starter intent taxonomy

Tenants extend this; these are the defaults that ship.

**Pre-sales and inspiration** — `destination_recommendation` · `product_information` · `price_or_availability_request` · `group_or_event_enquiry` · `brochure_or_catalogue_request`

**Booking lifecycle** — `booking_status` · `document_request` (tickets, vouchers, invoices) · `payment_status_or_method` · `instalment_or_balance_question` · `name_or_detail_correction` · `change_request` (hotel, room, dates, flight, passengers) · `cancellation_request` · `add_on_purchase` (excursions, transfers, luggage, seats, insurance) · `special_request` (dietary, accessibility, cot, celebration, room location)

**Travel information** — `flight_schedule_and_checkin` · `baggage_rules` · `transfer_information` · `hotel_information` · `excursion_information` · `destination_practical_info` (weather, currency, plugs, tipping, safety) · `visa_passport_health_requirements` · `travel_advisory_question`

**Post-travel and issues** — `complaint` · `compensation_or_refund_claim` · `lost_property_or_baggage` · `insurance_claim_support` · `positive_feedback` · `review_response`

**Administrative and other** — `data_subject_request` · `invoice_or_accounting` · `b2b_partner_enquiry` · `press_or_media` · `job_application` · `supplier_or_vendor` · `spam_or_irrelevant` · `unclear_needs_clarification`

### 8.2 Risk classes

| Class | Definition | Autonomy | Examples |
|---|---|---|---|
| **R0** | Public, non-binding, non-personal information | Auto-send eligible from L2 | Destination info, excursion descriptions, hotel facilities, baggage rules, general policy explanation |
| **R1** | Personal but read-only facts from a system of record | Auto-send eligible from L3, **strong verification required** | Departure time, booking status, document re-send, payment status |
| **R2** | Anything implying a commitment, a price, availability, or a change | **Never auto-send.** Draft + human | Change requests, cancellations, quotes, upgrades, goodwill |
| **R3** | Sensitive, legal, vulnerable or reputational | **Never auto-send.** Senior human, SLA-tracked | Complaints, compensation, illness/death, accessibility, minors, legal/regulator/press, DSARs |
| **R4** | Out of scope for a customer reply | Auto-classify and file; no reply or a fixed template | Spam, job applications, supplier invoices |

**Rule:** the risk class of a multi-intent message is the **highest** class among its parts.

### 8.3 Worked examples — your questions, resolved

| Customer question | Intent | Risk | Data needed | v1 behaviour |
|---|---|---|---|---|
| "I have not received the ticket reservation" | `document_request` | R1 | Booking status, document availability, release date, payment status | Verify identity (strong). If documents issued → re-send attached, auto-eligible at L3. If not yet released → state the exact release date from policy. If unpaid or on hold → **human**, with the reason surfaced. |
| "What can I do in Antalya?" | `destination_practical_info` | R0 | Destination content, excursion catalogue for the customer's dates if identified | Grounded answer from destination pages; auto-eligible from L2. Optionally lists bookable excursions available on their dates (cross-sell, off by default). |
| "What excursions can I go on at the destination?" | `excursion_information` | R0 → R1 if personalised | Excursion catalogue, validity windows; booking dates if identified | Auto-eligible if answering generally. If personalised to their dates, requires verification and current catalogue; **never quote a price unless it comes from the catalogue feed**. |
| "What are your best hotels for children?" | `product_information` / `destination_recommendation` | R0 | Hotel attribute data (kids' club, pools, family rooms, ages) | Auto-eligible **only if** structured hotel attributes exist. Without them the model will invent plausible-sounding facilities — so this intent stays human until the feed is ingested. A concrete example of why FR-M4-03 is a Must. |
| "What destinations do you recommend for youth?" | `destination_recommendation` | R0 | Product catalogue, marketing positioning, seasonality | Auto-eligible; a sales opportunity. Must recommend only products the operator actually sells, with valid departures. Route a copy to sales if configured. |
| "Can I change the hotel?" | `change_request` | **R2** | Change policy, fees, availability | **Never auto-sent.** Draft explains the applicable policy and required steps from the terms; the fee and availability are left as placeholders for the agent to fill from the reservation system. This is the guardrail in FR-M5-06 doing its job. |
| "When does the plane leave?" | `flight_schedule_and_checkin` | R1 | Live flight times from the reservation system | Requires strong verification and a **fresh** read (no cache) — schedules change. Auto-eligible from L3. If a change is detected versus the last communicated time, force human review. |

That table is the fastest way to explain the product to a prospect. It is also the fastest way to see why "one confidence score" was never going to work.

---
## 9. The AI pipeline and the autonomy gate

### 9.1 Pipeline stages

Each stage is independently observable, independently testable, and can fail closed.

| # | Stage | Output | Fails to |
|---|---|---|---|
| 1 | **Ingest** — fetch, parse, thread, dedupe, loop-check, attachment scan | Normalised Message on a Conversation | Quarantine + alert |
| 2 | **Screen** — spam/auto-reply/out-of-scope, injection detection, DMARC result | Proceed / file / force-human | Force human |
| 3 | **Understand** — language, intents (multi), entities, sentiment, urgency, risk class, hard-stop signals | Structured understanding record | Force human |
| 4 | **Identify** — booking resolution, verification level | Booking link + verification level | `unverified` |
| 5 | **Retrieve** — hybrid search over tenant knowledge, filtered by brand/language/validity; plus live reservation reads | Ranked, cited context set | Empty context → abstain |
| 6 | **Generate** — grounded draft in the customer's language and the tenant's voice, with per-claim citations | Draft + citations + uncertainty notes | Abstain |
| 7 | **Verify** — independent check for groundedness, contradiction, commitments, PII leakage, tone, language, injection compliance | Per-claim verdicts + flags | Force human |
| 8 | **Gate** — deterministic evaluation of every auto-send condition | `auto_send` / `human_review` / `abstain_and_escalate` | `human_review` |
| 9 | **Deliver** — send with disclosure and threading, or enqueue for the console | Sent message or queued case | Queue |
| 10 | **Observe** — log everything; sample for audit; feed metrics and the learning loop | Telemetry + audit records | — |

**Design note.** Stages 3, 6 and 7 use language models. Stages 2, 4 and 8 are mostly deterministic code. The decision to send is *never* made by a model — a model contributes evidence, and code applies the policy. That distinction is what makes the behaviour auditable and what you will say in every security review.

### 9.2 Auto-send gate — all conditions must pass

| # | Condition |
|---|---|
| G01 | Tenant autonomy level ≥ the level required for this intent, and the global kill switch is off |
| G02 | Intent is on the tenant's auto-send allowlist for the current level |
| G03 | Risk class ≤ the maximum permitted for this intent (never above R1) |
| G04 | No hard-stop signal detected (complaint, legal, medical, minor, press, DSAR, abuse, injection) |
| G05 | Composite confidence ≥ the per-intent threshold |
| G06 | Every factual claim is supported by a cited source or a system-of-record field, per the verifier |
| G07 | All cited sources are within their freshness TTL and within their validity window |
| G08 | If the answer contains personal data: verification level ≥ the level required by the disclosure matrix, and DMARC passed |
| G09 | If the answer contains time-critical facts: data was read live, not from cache |
| G10 | The commitment guardrail found no price, availability, fee, change confirmation or new obligation that did not come from a system of record |
| G11 | Draft language matches customer language, and the tenant has approved autonomy for that language |
| G12 | No human has taken over this thread; the customer is not on an exclusion list and has not asked for a human |
| G13 | Rate limits and per-recipient caps not exceeded; circuit breaker closed for this intent |
| G14 | Message passes automated safety and tone checks (no leaked prompt text, no internal notes, no other customer's data, no broken merge fields) |
| G15 | Configured time-window rules permit sending now |

Failing G01–G03 or G12 routes to the queue. Failing G04 routes to a specialist queue. Failing G05–G07 or G10 routes to review **with the failure reason shown to the agent** — this is what makes agents trust the system rather than resent it.

### 9.3 Composite confidence

A single number is required for thresholds, but it must be assembled from independent evidence and, crucially, **calibrated against observed correctness** rather than taken from the model's self-report.

Inputs: intent classifier margin · retrieval score of the top supporting chunks · coverage (proportion of the question addressed by retrieved context) · verifier groundedness score · self-consistency across sampled generations for high-value intents · historical accuracy for this intent, tenant and language.

Requirements:

- **CAL-01** Confidence must be calibrated per tenant and per intent against audited outcomes, and re-calibrated on a schedule. Report calibration error; an uncalibrated score is not permitted to gate sends.
- **CAL-02** Thresholds are set to hit a **target precision**, not a target automation rate. The buyer chooses the precision (default: ≥ 98% of auto-sent messages rated correct in audit); the automation rate is whatever that allows.
- **CAL-03** Until a tenant has enough audited cases for calibration (default: 200 per intent), that intent cannot exceed L1.
- **CAL-04** Confidence must be displayed to agents as a band (high/medium/low) with the reasons, not as false-precision decimals.

### 9.4 Model strategy

- **MOD-01** Model-agnostic architecture behind an internal interface; no business logic depends on a specific provider.
- **MOD-02** Use a tiered approach: small/cheap models for classification, screening and routing; a strong model for generation and verification. Cost per conversation is a tracked product metric.
- **MOD-03** The verifier should be a different model or at minimum a separate call with a different prompt and no access to the generator's reasoning — a model checking its own work is worth little.
- **MOD-04** All prompts are versioned artefacts under change control, evaluated against the frozen set before rollout, with canary release and instant rollback.
- **MOD-05** Provider outage or degradation must fail to human review, never to a lower-quality autonomous answer.
- **MOD-06** Pin model versions per tenant; provider model updates are a change subject to the regression gate, not something that happens to you silently.
- **MOD-07** Treat all retrieved content and all customer text as **data, never instructions**, with structural separation in the prompt and injection testing in the eval set.

---

## 10. Data model (core entities)

| Entity | Key attributes | Notes |
|---|---|---|
| **Tenant** | id, name, plan, region, retention policy, autonomy level, languages, currency | Isolation boundary for everything |
| **Brand** | tenant, name, domain, signature, tone profile, knowledge scope | Operators run several brands |
| **Mailbox** | tenant, brand, provider, credentials (vaulted), state, sending identity | |
| **Conversation** | tenant, brand, customer, booking?, status, intents, risk class, assignee, SLA due, tags, autonomy outcome | The unit of work and of billing |
| **Message** | conversation, direction, from/to, headers, body (text/html), attachments, auth results, language | Immutable once stored |
| **Attachment** | message, filename, type, size, storage ref, scan result, extracted text, PII flags | |
| **Customer** | tenant, emails, names, phone, consent/exclusion flags, language preference | Subject to erasure |
| **Booking** *(cached projection)* | tenant, external id, reference, status, dates, destination, accommodation, transport, pax, payment state, documents, policy | Short TTL; never the source of truth |
| **Understanding** | message, language, intents+scores, entities, sentiment, urgency, risk class, hard-stop flags, model versions | One per message; immutable |
| **KnowledgeSource** | tenant, type, location, crawl/ingest config, TTL, owner, status | |
| **KnowledgeItem** | source, content, embedding, metadata (language, brand, destination, product, validity window, authority tier, last verified), status | |
| **Draft** | conversation, content, language, citations, uncertainty notes, model+prompt versions, confidence components | Multiple per conversation over time |
| **Citation** | draft, claim span, knowledge item or booking field, score | Enables the evidence panel |
| **GateEvaluation** | draft, per-condition results, outcome, timestamp | The auditable send decision |
| **ReviewAction** | draft, user, action, diff, edit distance, reason code, comment, timestamp | The learning-loop input |
| **SentMessage** | conversation, content, sender (agent or system), disclosure text, model versions, delivery status | |
| **AuditRecord** | tenant, actor, action, object, before/after, timestamp, ip | Immutable, independently retained |
| **Event** *(crisis)* | tenant, title, official position (versioned), clusters, affected bookings, status | |
| **EvaluationCase** | tenant, input, expected answer, intent, tags, version | The frozen set |
| **AutonomyPolicy** | tenant, brand, intent, level, threshold, max risk, languages, time windows, version, changed-by | Versioned; changes are audited |

---

## 11. Non-functional requirements

### 11.1 Performance

| ID | Requirement |
|---|---|
| NFR-P-01 | New mail enters the pipeline within **60 s** of arrival (p95). |
| NFR-P-02 | Draft ready within **90 s** of ingest (p95), **180 s** (p99). |
| NFR-P-03 | Auto-sent replies dispatched within **5 minutes** of receipt (p95), including the configurable hold delay. |
| NFR-P-04 | Console: case list loads < 1.5 s (p95); opening a case < 1 s (p95); send action acknowledged < 500 ms. |
| NFR-P-05 | Search across a tenant's full case history returns in < 2 s (p95). |

### 11.2 Scale and availability

| ID | Requirement |
|---|---|
| NFR-S-01 | Sustain 20,000 inbound messages/day per large tenant; absorb a **10× burst for 6 hours** without queue collapse or degraded correctness (throughput may degrade; accuracy may not). |
| NFR-S-02 | Availability: 99.5% for ingest and console (standard tier), 99.9% (enterprise tier), excluding third-party provider outages, which must be reported and must degrade to human review. |
| NFR-S-03 | Horizontal scaling of pipeline workers independent of the console. |
| NFR-S-04 | No message may be lost. At-least-once processing with idempotent sends — a duplicate reply to a customer is a **P1 defect**. |
| NFR-S-05 | RPO ≤ 15 minutes, RTO ≤ 4 hours. Tested restore at least annually. |

### 11.3 Reliability and observability

| ID | Requirement |
|---|---|
| NFR-R-01 | Every pipeline stage emits structured telemetry with a correlation id spanning the whole case. |
| NFR-R-02 | Poison messages are quarantined, alerted and replayable after a fix; they never block the queue. |
| NFR-R-03 | Alerting on: ingest lag, gate failure-rate anomalies, circuit-breaker trips, connector failures, crawl failures, model error rates, cost anomalies. |
| NFR-R-04 | Full reprocessing capability: re-run classification/retrieval/generation on historical cases for evaluation, without any risk of re-sending. Send must be physically impossible in replay mode. |

### 11.4 Data retention (defaults; tenant-configurable)

| Data class | Default retention | Note |
|---|---|---|
| Conversations and messages | 24 months | Common travel dispute horizon; tenant may shorten or extend within legal limits |
| Attachments | 12 months | Identity documents: 30 days unless the tenant justifies longer |
| Booking cache | 30 days after departure | Never the source of truth |
| Drafts, citations, gate evaluations | 24 months | Needed for audit and dispute |
| Audit log | 24 months minimum, retained through case deletion where lawful | |
| Model prompts/completions with PII | 30 days | Debugging only, access-controlled |
| Aggregated, non-identifying metrics | Indefinite | |
| Evaluation set | Life of tenant | Personal data pseudonymised on entry |

---

## 12. Security requirements

| ID | Requirement |
|---|---|
| SEC-01 | Encryption in transit (TLS 1.2+) and at rest (AES-256) for all data stores, backups and search indexes. |
| SEC-02 | Mailbox and connector credentials in a managed secrets vault; OAuth preferred over stored passwords; automatic rotation where supported. |
| SEC-03 | Least-privilege service accounts; the reservation connector is read-only in v1 and must be granted only the fields the product uses. |
| SEC-04 | Tenant isolation enforced at the data layer (row-level security or per-tenant schemas) plus application checks; **cross-tenant retrieval is a P0 defect with a mandatory incident report**. |
| SEC-05 | SSO (SAML/OIDC) with enforced MFA for privileged roles; session timeouts; device/IP restrictions optional per tenant. |
| SEC-06 | Full audit logging of access to customer data, including vendor support access, with tenant-visible reporting. |
| SEC-07 | Malware scanning of all inbound attachments before storage or extraction; sandboxed extraction. |
| SEC-08 | Egress controls: the crawler and connectors may only reach allowlisted destinations; no arbitrary URL fetching driven by email content. |
| SEC-09 | Prompt-injection defences tested as a standing part of the evaluation set, including injection via attachments and via crawled pages. |
| SEC-10 | Annual third-party penetration test; dependency and container scanning in CI; documented vulnerability SLA. |
| SEC-11 | Secure SDLC: code review, no production data in development, separate environments, break-glass procedure with alerting. |
| SEC-12 | Rate limiting and abuse protection on all public endpoints, including inbound mail (a hostile sender must not be able to exhaust a tenant's budget). |

---

## 13. Compliance and legal

### 13.1 Data protection (GDPR)

The product processes personal data of the operator's customers. The operator is the **controller**; the vendor is a **processor**; the model provider is a **sub-processor**.

| ID | Requirement |
|---|---|
| LEG-01 | Standard DPA with defined processing purposes, sub-processor list, international-transfer safeguards and audit rights. |
| LEG-02 | EU data residency for storage, retrieval and inference for EU tenants (FR-M13-08). |
| LEG-03 | Contractual and technical guarantee that tenant data is not used to train the vendor's or the model provider's models. |
| LEG-04 | Data-protection-by-design documentation: data flow diagrams, categories processed, minimisation measures, retention — enough for the tenant's DPIA, which they will have to do. **Provide a DPIA template as a sales asset.** |
| LEG-05 | Erasure and export tooling covering primary stores, search indexes, caches, backups (documented lag), logs and the evaluation set. |
| LEG-06 | Special-category data (health, disability, dietary indicating religion) is common in this domain: detect, minimise, restrict, and never use for automated decisions. |

### 13.2 EU AI Act — transparency

Article 50 transparency obligations have applied since **2 August 2026**. An AI system that interacts directly with people must inform them that they are dealing with AI unless that is obvious, and generated synthetic content carries marking obligations (with a transitional period reported to 2 December 2026 for systems already on the market). Non-compliance exposure is reported at up to €15 million or 3% of worldwide annual turnover.

| ID | Requirement |
|---|---|
| LEG-07 | Every customer-facing message generated by the system carries a clear AI disclosure, configurable in wording and placement but not removable below the legal minimum for in-scope tenants. |
| LEG-08 | Where a human materially reviews and takes responsibility for a message, the disclosure wording may differ; the distinction must be recorded per message and defensible. **Decide the policy deliberately — see OD-09.** |
| LEG-09 | Machine-readable marking of AI-generated content, plus a per-message record of model, prompt version and generation timestamp. |
| LEG-10 | Human-oversight evidence per message (FR-M13-11), and documentation of the system's intended purpose, limitations and known failure modes — supplied to tenants as deployer documentation. |
| LEG-11 | Track the applicable Code of Practice on transparency and assess signing it, since alignment is reported to carry a more favourable enforcement posture. |
| LEG-12 | Maintain a classification assessment of the system under the AI Act (the working assumption is limited-risk/transparency obligations, **not** high-risk — but this must be documented, reviewed with counsel, and revisited if the product ever makes decisions affecting access to essential services or performs emotion recognition). |

> **Not legal advice.** These are product requirements derived from public summaries of the regulation. The classification in LEG-12 and the disclosure policy in LEG-08 must be confirmed with a qualified lawyer before go-live.

### 13.3 Package travel rules

The revised Package Travel Directive was adopted by the Council on **30 March 2026**. Reported changes relevant to this product: mandatory complaint-handling arrangements before, during and after travel; refunds within **14 days** on cancellation (longer, defined windows in insolvency); vouchers permitted with conditions (value, 12-month validity, single transfer, insolvency protection); a cap reported at **25%** on advance payments; and expanded information duties covering payment methods, passport and visa requirements, accessibility and cancellation fees. Member-state transposition follows, with application dates later in the decade — so this is a **design-for** requirement, not a ship-blocker.

| ID | Requirement |
|---|---|
| LEG-13 | Complaint register with timestamps, ownership, deadlines, status and export — satisfying "transparent, documented processes with verifiable deadline compliance". |
| LEG-14 | Configurable response-deadline rules per complaint type, with escalation on breach. |
| LEG-15 | Never auto-answer a complaint or a compensation claim (also FR-M13-03). |
| LEG-16 | Where the tenant supplies structured policy content, the system may explain rights and timelines — quoting the tenant's own approved text, never a model's paraphrase of the law. |
| LEG-17 | Because pre-contractual information given by an organiser can bind them, the commitment guardrail (FR-M5-06) is a legal control, not just a quality control. Document it as such. |

---

## 14. Commercial model and success metrics

### 14.1 Packaging (proposal)

| Tier | For | Includes |
|---|---|---|
| **Starter** | Small operators, one brand | 1 mailbox, website + document knowledge, no reservation integration, L0–L1 only, 3 seats, standard SLA |
| **Growth** | Mid-size, multi-market | Multiple mailboxes/brands, reservation connector, full autonomy ladder, 10 seats, analytics, crisis mode |
| **Enterprise** | Large operators | Unlimited brands, SSO/SCIM, custom connector, 99.9% SLA, data-residency options, dedicated support, security review support |

**Pricing shape (recommended):** a monthly platform fee per tier + a usage fee per **conversation handled** (not per email), with bundled volume and overage. Optionally a lower rate for conversations that are only triaged and a higher rate for auto-resolved ones.

Why not pure per-resolution: it is the most attractive story ("you only pay when it works") but it inverts the incentive on abstention — the product must be free to abstain, and a per-resolution price quietly punishes exactly the behaviour that keeps the buyer safe. Consider it as a marketing frame on top of conversation-based billing rather than as the billing mechanic. *(→ OD-12)*

**Additional revenue:** onboarding and knowledge-setup fee; custom connector development; premium language enablement; annual compliance-reporting add-on.

**Seasonality:** tour operator volume is extreme and lumpy. Annual contracts with an annual volume bundle (not monthly caps) prevent a customer being punished for a good booking season, and protect you from churn in the quiet months.

### 14.2 Unit economics — the model to fill in

Gross margin depends almost entirely on model cost per conversation, so this must be instrumented from day one, not estimated once.

```
cost_per_conversation =
      classification_calls  × tokens × rate
    + retrieval/embedding   × tokens × rate
    + generation_calls      × tokens × rate
    + verification_calls    × tokens × rate
    + infrastructure_per_conversation
```

Realistic starting assumptions to validate in the pilot: **3–6 model calls per conversation**; a generation call carrying roughly 4,000–10,000 input tokens (email + thread + retrieved chunks + instructions) and 300–800 output tokens; classification and verification materially cheaper if a smaller model is used.

| ID | Requirement |
|---|---|
| ECO-01 | Track and report cost per conversation, per tenant, per intent, continuously. |
| ECO-02 | Alert on cost anomalies (a runaway thread, an oversized attachment, a retrieval blowup). |
| ECO-03 | Enforce per-tenant spend caps with degradation to human-review rather than uncontrolled spend. |
| ECO-04 | Optimise deliberately: cache retrieval for repeated questions, use small models for screening, trim thread history, and reuse canonical answers verbatim where they match — a matched canonical answer should cost near zero. |
| ECO-05 | Target gross margin must be agreed before pricing is published *(→ OD-12)*; the pilot exists partly to produce this number. |

### 14.3 Product success metrics

| Metric | Definition | Target by month 6 of a tenant's life |
|---|---|---|
| **Automation rate** | Auto-sent ÷ all customer-answerable conversations | 30–50% (mix-dependent; higher without an integration is a warning sign, not a win) |
| **Auto-send precision** | % of audited auto-sent messages rated correct and appropriate | ≥ 98% — the number the whole product is tuned to |
| **Assist adoption** | % of human-sent replies that started as a system draft | ≥ 85% |
| **Median edit distance** | Similarity between draft and sent message | ≤ 15% |
| **First response time** | Receipt → first substantive reply | < 5 min auto; < 2 h assisted (from a baseline that is usually hours to days) |
| **Handling time** | Agent minutes per conversation | −50% vs baseline |
| **Abstention rate** | Conversations where the system declined to draft | Falling month over month as knowledge fills |
| **Knowledge gap closure** | Backlog items resolved ÷ raised | > 1.0 |
| **Escalation-after-auto rate** | Customers who reply to an auto-sent answer needing more help | < 10% |
| **CSAT** | Where measured | Not below the human baseline |

### 14.4 Tenant acceptance criteria (contractual go-live gates)

- Shadow mode run of ≥ 4 weeks or ≥ 1,000 conversations, whichever comes first.
- Blind review of ≥ 200 shadow drafts by the tenant's own senior agents, with ≥ 95% rated "would have sent, with no or trivial edits" for the intents proposed for auto-send.
- Zero critical failures in shadow: no fabricated commitment, no cross-customer data exposure, no unsafe answer.
- Sending domain authentication verified; disclosure text approved; DPA and DPIA complete.
- Kill switch and escalation path tested with the tenant's team.

---

## 15. Roadmap and phasing

Sequenced so that each phase is independently demonstrable and each phase de-risks the next. Durations are placeholders until a team is sized.

### Phase 0 — Design partner and evidence *(before building much)*

Secure one operator willing to give you a real mailbox archive. Manually classify 500–1,000 real emails to get the true intent mix and automatable share. **This single exercise determines whether the product is worth building and what its automation ceiling actually is.** Everything below is guesswork without it.

### Phase 1 — "Read-only value": triage and shadow

Mail ingest, threading, loop protection, understanding, knowledge ingestion from website and documents, retrieval, generation, verification, and the console in **shadow mode only** — no sending. Case list, filters, audit trail. Metrics: agreement with what the human actually sent.

*Sellable as:* a triage and drafting assistant. *Proves:* the classification and the answer quality, with zero risk to the customer relationship. This is also the best possible sales instrument — you can run it on a prospect's inbox and show them what it would have done.

### Phase 2 — "Assisted send": humans in the loop

Full review and send workflow, SLA timers, assignment, escalation, edit capture, tone profile, knowledge gaps backlog, translation view, basic analytics and ROI view.

*Sellable as:* a complete AI-assisted support desk. *Most of the buyer's efficiency gain arrives here* — and it arrives without any autonomy risk. Do not rush past this phase; a large share of prospects will be happy to stop here, and they should be allowed to.

### Phase 3 — "Narrow autonomy": R0 auto-send

Autonomy gate, trust ladder L0–L2, calibration, circuit breaker, kill switch, post-send audit sampling, AI disclosure, rate limits and hold-delay.

*Sellable as:* the automation promise, delivered conservatively. Restricted to R0 content intents.

### Phase 4 — "Booking-aware": integration and R1 autonomy

Reservation connector framework, reference connector, identification and verification, disclosure matrix, document re-send, live schedule reads, L3 autonomy for R1 intents.

*This is where the automation rate roughly doubles* — and where the security surface does too.

### Phase 5 — "Scale and resilience"

Crisis mode, proactive outbound, multi-brand, SSO/SCIM, self-serve onboarding, connector conformance suite, compliance reporting pack, helpdesk integrations.

### Phase 6+ — Expansion

Additional channels (chat, WhatsApp), write-back operations, deeper cross-sell, per-tenant model tuning if and only if evidence supports it.

---

## 16. Risk register

| ID | Risk | Impact | Mitigation |
|---|---|---|---|
| RSK-01 | **Confidently wrong answer** on a booking-critical fact (departure time, ticket status) | Customer misses a flight; operator liability; contract lost | Live reads only, no cache for time-critical facts; verification pass; strong identity; conservative thresholds; hold delay; 100% audit at L2 |
| RSK-02 | **Fabricated commitment** (price, upgrade, waiver) | Legal obligation; direct financial loss | FR-M5-06 deterministic guardrail; R2 never automated; commitment values only from systems of record |
| RSK-03 | **Stale or wrong source content** — the operator's own website is out of date | Wrong answers at scale, blamed on the AI | Freshness TTLs; validity windows; exclusion of stale sources from auto-send; content-owner review queue; onboarding content audit |
| RSK-04 | **Identity error** → personal data sent to the wrong person | GDPR breach, notification obligations | Verification levels; disclosure matrix; DMARC checks; contact-on-booking rule; audit of identity decisions |
| RSK-05 | **Prompt injection** via email body or attachment | Policy bypass, data exfiltration, embarrassing output | Data/instruction separation; injection detection; standing red-team cases in the eval set; verifier; egress controls |
| RSK-06 | **Auto-reply loop** with a customer's out-of-office | Thousands of emails, blacklisting, ridicule | Header checks, per-address caps, loop detection (FR-M1-06) |
| RSK-07 | **Over-trust after a good month** — supervisor raises autonomy too far | Sudden quality collapse | Promotion gated on measured criteria; circuit breaker; sampled audit at every level |
| RSK-08 | **Automation rate below expectation**, killing the ROI case | Churn, refunds, bad references | Sell Phase 2 value honestly; measure intent mix in Phase 0; never promise a rate before the shadow run |
| RSK-09 | **Model provider change**: price rise, deprecation, outage, policy change | Margin or availability shock | Provider-agnostic interface; pinned versions; second provider qualified; degrade to human review |
| RSK-10 | **Seasonal cost spike** exceeds contracted revenue | Negative gross margin in peak months | Annual volume bundles; spend caps; cost-per-conversation alerting; caching and canonical-answer reuse |
| RSK-11 | **Agent resistance** — staff see it as a threat and quietly avoid it | Adoption failure despite technical success | Involve agents in shadow review; measure and celebrate time saved, not headcount cut; make the console genuinely faster than their old workflow; keep classification overridable |
| RSK-12 | **Integration is harder than promised** for a given operator | Delayed value, blown implementation budget | Degraded mode as a real, supported product state; connector conformance suite; file-drop fallback |
| RSK-13 | **Regulatory shift** (AI Act guidance, PTD transposition detail) | Rework, blocked sales | Configurable disclosure; versioned policy; counsel review before go-live; track the transparency Code of Practice |
| RSK-14 | **Minority-language quality** is materially worse than the majority languages | Poor answers in exactly the markets the buyer wanted help with | Per-language autonomy approval; per-language eval sets and metrics; native-speaker review at onboarding |
| RSK-15 | **Cross-tenant leakage** in retrieval or analytics | Existential for a B2B product | Data-layer isolation, automated isolation tests in CI, P0 severity, mandatory incident reporting |

---

## 17. Open decisions

These are the gaps I cannot close for you. Each needs an owner and a date. Answering them turns this into v1.0.

### Product and market

- **OD-01 — Product name and brand.** "TourDesk AI" is a placeholder.
- **OD-02 — Beachhead market.** Which country/language first? This drives language priorities, legal review and the first connector. *(Your Tourpaq context suggests a Nordic/Romanian operator profile — worth confirming, because it also decides whether a Tourpaq connector is the reference implementation.)*
- **OD-03 — Design partner.** Who is operator #1, and will they give you a mailbox archive for Phase 0? Without this the automation-rate assumptions in §14.3 are fiction.
- **OD-04 — Standalone or helpdesk add-on?** Do you replace the tenant's inbox workflow, or plug into Zendesk/Freshdesk/HubSpot? This is the single biggest architectural fork in the document — it changes the console from a core asset to a thin layer.
- **OD-05 — Segment.** Small operators (self-serve, low price, high volume of customers) or mid/large (integration projects, high ACV, long sales cycles)? The product can serve both eventually; it cannot be built for both first.

### Scope and behaviour

- **OD-06 — Is pre-sales in v1?** Treating enquiries as revenue changes the metrics, the buyer (sales, not service) and the roadmap.
- **OD-07 — Which intents are on the day-one auto-send allowlist?** My proposal: `destination_practical_info`, `excursion_information` (general), `baggage_rules`, `product_information`, `visa_passport_health_requirements` (pointing at official sources, never advising) — all R0, all content-only.
- **OD-08 — Cross-sell in replies: yes, no, or tenant-configurable?** Revenue upside versus the perception of an AI upselling a customer who asked a simple question. My recommendation: build it, default it off, and let the tenant switch it on per intent.
- **OD-09 — Disclosure policy.** Does a human-reviewed, human-edited message still carry an AI disclosure? Legally arguable; commercially sensitive; needs counsel. *(→ LEG-08)*
- **OD-10 — Send delay.** Do auto-sends wait 60 seconds (safety, recallable) or go immediately (the "instant answer" wow factor)? My recommendation: configurable, default 60 s.
- **OD-11 — Who owns the mailbox?** Does the product become the tenant's primary inbox, or does it work alongside their existing client? Coexistence is much harder but much easier to sell into a nervous team.

### Commercial

- **OD-12 — Pricing mechanic and target gross margin.** Platform + per-conversation is my recommendation (§14.1); the margin target must be set before pricing is published.
- **OD-13 — Does the vendor sell services?** Knowledge setup, content cleanup and connector work are real revenue and real distraction.
- **OD-14 — Contract shape.** Annual with a volume bundle, given the seasonality?

### Technical

- **OD-15 — Build vs. buy for the platform substrate.** Vector store, search, queueing, mail parsing, crawler: which are bought? Recommendation: buy everything except the pipeline, the gate and the console — those three are the product.
- **OD-16 — Model provider(s) and data-residency posture.** Which providers meet the EU-residency and no-training commitments you intend to sell?
- **OD-17 — Reference reservation connector.** Which back-end first, and is an API available or is this a database/export integration?
- **OD-18 — Team and timeline.** Everything in §15 is unsequenced in real time until this is known.

---

## 18. Glossary

| Term | Meaning |
|---|---|
| **Abstain** | The system declines to draft or send because grounding is insufficient. A success state. |
| **Autonomy level (L0–L4)** | The trust ladder position of a tenant/intent, governing what may be auto-sent. |
| **Canonical answer** | A human-approved, reusable answer stored as a top-authority knowledge item. |
| **Composite confidence** | Calibrated score assembled from retrieval, verification and historical accuracy — not the model's self-reported certainty. |
| **Edit distance** | How much a human changed a draft before sending; the primary quality proxy. |
| **Frozen evaluation set** | Versioned, held-out cases used to prove that changes improve rather than regress. |
| **Gate** | The deterministic set of conditions that must all pass before an auto-send. |
| **Grounding** | Every factual claim traceable to a retrieved source or a system-of-record field. |
| **Hard stop** | A signal that forces human handling regardless of confidence. |
| **Risk class (R0–R4)** | The damage potential of answering a given intent wrongly. |
| **Shadow mode** | The system drafts but cannot send; used to measure quality safely before go-live. |
| **System of record** | The authoritative source for a fact — normally the reservation system, never the model. |
| **Verification level** | How confident the system is that the sender is entitled to the booking's data. |

---

### Sources for the regulatory statements in this document

- EU AI Act Article 50 transparency obligations, effective 2 August 2026, disclosure and marking duties, transitional period to 2 December 2026, penalties up to €15m/3% — [Cooley](https://www.cooley.com/news/insight/2026/2026-08-03-eu-ai-act-transparency-obligations-take-effect-2-august-2026), [European Commission](https://digital-strategy.ec.europa.eu/en/faqs/transparency-obligations-under-article-50-ai-act), [artificialintelligenceact.eu](https://artificialintelligenceact.eu/transparency-rules-article-50/)
- Revised Package Travel Directive adopted 30 March 2026; complaint-handling arrangements, 14-day refunds, voucher conditions, information duties — [Council of the EU](https://www.consilium.europa.eu/en/policies/package-travel/), [KPMG Law](https://kpmg-law.de/en/new-package-travel-directive-2026-complaint-management-becomes-mandatory/), [ABTA](https://www.abta.com/news/ask-expert-revised-eu-package-travel-directive-and-how-it-may-affect-you)

*Regulatory content is summarised from public sources for product-planning purposes and is not legal advice.*
