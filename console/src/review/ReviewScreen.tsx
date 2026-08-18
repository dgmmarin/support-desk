import { useEffect, useState } from "react";
import type { ApiClient } from "../api/client";
import type { ReviewSurface } from "../api/types";
import { ThreadPane } from "./ThreadPane";
import { EvidencePane } from "./EvidencePane";
// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function ReviewScreen({ client, conversationId, agent: _agent, onDone }: { client: ApiClient; conversationId: string; agent: string; onDone: () => void }) {
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
          : <div data-testid="draft-region">{surface.draft}</div>}
      </div>
      <EvidencePane surface={surface} />
      <button onClick={onDone} style={{ position: "absolute", right: 8, top: 8 }}>Back to queue</button>
    </div>
  );
}
