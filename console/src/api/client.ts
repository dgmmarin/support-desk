import type { Session } from "../session/SessionContext";
import type { ActRequest, ActResult, QueueItem, ReviewSurface } from "./types";

export class ApiError extends Error {
  constructor(public status: number, msg: string) {
    super(msg);
  }
}
export class PermissionError extends ApiError {}
export class ClaimConflict extends ApiError {}
export class MissingTenant extends ApiError {}

type FetchImpl = typeof fetch;

type ClaimResult = { conversation_id: string; agent: string; expires_at: string };
type ResolveResult = { resolved: boolean };

export function makeClient(getSession: () => Session | null, fetchImpl: FetchImpl = fetch) {
  async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
    const s = getSession();
    if (!s?.tenantId || !s?.token) throw new MissingTenant(400, "no session");
    const res = await fetchImpl(path, {
      method,
      headers: {
        "X-Tenant-ID": s.tenantId,
        Authorization: `Bearer ${s.token}`,
        "Content-Type": "application/json",
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.status === 403) throw new PermissionError(403, "insufficient role");
    if (res.status === 409) throw new ClaimConflict(409, "already claimed");
    if (res.status === 400) throw new MissingTenant(400, "missing tenant/bad request");
    if (!res.ok) throw new ApiError(res.status, `request failed (${res.status})`);
    return (res.status === 204 ? undefined : await res.json()) as T;
  }

  return {
    getQueue: async (): Promise<QueueItem[]> =>
      (await call<{ items: QueueItem[] }>("GET", "/queue")).items ?? [],

    // ponytail: `agent = "me"` is a placeholder identity — M11 wires the real
    // agent id from the authenticated session; until then every claim/resolve
    // call is attributed to a literal "me" unless a caller overrides it.
    claim: (conversationId: string, agent = "me"): Promise<ClaimResult> =>
      call<ClaimResult>("POST", "/queue/claim", { conversation_id: conversationId, agent }),

    resolve: (conversationId: string, agent = "me"): Promise<ResolveResult> =>
      call<ResolveResult>("POST", "/queue/resolve", { conversation_id: conversationId, agent }),

    getReview: (conversationId: string): Promise<ReviewSurface> =>
      call<ReviewSurface>("GET", `/queue/review?conversation_id=${encodeURIComponent(conversationId)}`),

    // Wire field is `content`, not `edited_body` (backend/internal/queue/act.go
    // actRequest) — the brief's default mapping assumed a field that doesn't exist.
    act: (req: ActRequest): Promise<ActResult> =>
      call<ActResult>("POST", "/queue/act", {
        conversation_id: req.conversationId,
        agent: req.agent,
        action: req.action,
        content: req.editedBody,
        target_queue: req.targetQueue,
        reason: req.reason,
        reason_code: req.reasonCode,
        comment: req.comment,
      }),
  };
}

export type ApiClient = ReturnType<typeof makeClient>;
