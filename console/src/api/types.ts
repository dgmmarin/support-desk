// Wire types for the M7 agent-console API (backend/internal/queue/*.go,
// backend/internal/review/surface.go). Field spellings here are the ACTUAL
// `json:` tags on the Go response structs — this file is the single source
// of truth for wire shapes, not a client-side convenience shape. Where the
// backend has no camelCase equivalent (e.g. no `level` field on the
// autonomy indicator, no `snippet` on evidence sources), the type reflects
// what the backend actually sends; see task-3-report.md for the full list
// of deviations from the originally sketched shape.

// --- Queue (backend/internal/queue/queue.go) ---

export type Sla = {
  defined: boolean;
  target_at?: string;
  remaining_secs: number;
  window_secs?: number;
  breached: boolean;
};

export type QueueItem = {
  conversation_id: string;
  score: number;
  risk_class: number;
  intent?: string;
  channel?: string;
  enqueued_at: string;
  departure_at?: string;
  sla: Sla;
  status: string;
  queue: string;
  locked: boolean;
  claimed_by?: string;
};

// --- Review surface (backend/internal/review/surface.go: Surface) ---

export type Message = {
  from: string;
  direction: string;
  subject?: string;
  body: string;
  automated: boolean;
};

// Wire name is InlineCitation (review.InlineCitation) — carries a `resolved`
// flag the brief's default shape omitted.
export type Citation = {
  claim_span: string;
  knowledge_item_id?: string;
  booking_field_path?: string;
  score?: number;
  resolved: boolean;
};

export type EvidenceSource = {
  id: string;
  title?: string;
  url?: string;
  score?: number;
};

// review.BookingPanel is a flat set of specific reservation fields, not a
// generic available/reason/fields map.
export type BookingPanel = {
  available: boolean;
  withheld: boolean;
  reason?: string;
  ref?: string;
  status?: string;
  destination?: string;
  dates?: string[];
  payment_status?: string;
  balance_due?: string;
  accommodation?: string;
  transport?: string;
};

// gate.Condition has no json tags, so Go's default marshalling uses the
// exported field names verbatim (capitalised).
export type GateCondition = { ID: string; Pass: boolean; Detail: string };

// review.AutonomyIndicator has no `level` field — the outcome/route pair is
// the closest analogue the backend actually exposes.
export type AutonomyIndicator = {
  present: boolean;
  outcome?: string;
  route?: string;
  auto_send_eligible: boolean;
  confidence_band?: string;
  conditions?: GateCondition[];
  reasons_for_agent?: string[];
};

export type TranslationView = {
  customer_language?: string;
  draft_language?: string;
  original_message: string;
  draft: string;
  mt_available: boolean;
  machine_translation?: string;
  back_translation?: string;
  note?: string;
};

export type ReviewSurface = {
  conversation_id: string;
  customer_message: Message;
  thread: Message[];
  draft_available: boolean;
  draft: string;
  draft_language?: string;
  draft_status?: string;
  inline_citations: Citation[];
  unsupported_claims?: string[];
  evidence: EvidenceSource[];
  booking: BookingPanel;
  autonomy: AutonomyIndicator;
  translation: TranslationView;
};

// --- Act (backend/internal/queue/act.go) ---
// Only the four actions the backend actually dispatches (serveAct's switch).
// snooze/reassign/mark_spam/request_info are explicitly out of scope per the
// act.go doc comment — including them here would type-check a request the
// server always 400s.
export type ActionKind = "approve_send" | "edit_send" | "reject" | "escalate";

// Client-side request DTO; act() in client.ts maps this to the Go
// actRequest's snake_case body (edited body -> `content`, not `edited_body`).
export type ActRequest = {
  conversationId: string;
  agent: string;
  action: ActionKind;
  editedBody?: string;
  targetQueue?: string; // required by the backend when action === "escalate"
  reason?: string; // escalate / reject
  reasonCode?: string; // edit_send feedback reason
  comment?: string;
};

// Wire shape of act.go's actResponse — no generic `message` field; the
// backend reports outcome via these specific booleans/ids instead.
export type ActResult = {
  action: ActionKind;
  sent?: boolean;
  already_sent?: boolean;
  sent_message_id?: string;
  escalated?: boolean;
  rejected?: boolean;
};
