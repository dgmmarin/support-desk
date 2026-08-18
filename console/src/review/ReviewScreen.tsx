import { useEffect, useState } from "react";
import type { ApiClient } from "../api/client";
import type { ActionKind, ReviewSurface } from "../api/types";
import { ThreadPane } from "./ThreadPane";
import { EvidencePane } from "./EvidencePane";
import { DraftEditor } from "./DraftEditor";
import { ActionBar } from "./ActionBar";

export function ReviewScreen({ client, conversationId, agent, onDone }: { client: ApiClient; conversationId: string; agent: string; onDone: () => void }) {
  const [surface, setSurface] = useState<ReviewSurface | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { client.getReview(conversationId).then(setSurface).catch(() => setError("could not load case")); }, [client, conversationId]);
  if (error) return <p role="alert">{error}</p>;
  if (!surface) return <p>Loading…</p>;
  return (
    <div style={{ display: "grid", gridTemplateColumns: "1fr 1.2fr 1fr", gap: "var(--pane-gap)", height: "100%", position: "relative" }}>
      <ThreadPane customerMessage={surface.customer_message} thread={surface.thread} />
      <div aria-label="draft">
        {!surface.draft_available
          ? <p role="status" style={{ color: "var(--warn)" }}>{surface.draft_status ?? "abstained / escalated"}</p>
          : <DraftRegion client={client} surface={surface} agent={agent} onDone={onDone} />}
      </div>
      <EvidencePane surface={surface} />
      <button onClick={onDone} style={{ position: "absolute", right: 8, top: 8 }}>Back to queue</button>
    </div>
  );
}

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
        targetQueue: action === "escalate" ? "specialist" : undefined,
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
