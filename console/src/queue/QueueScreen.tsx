import { useState } from "react";
import type { ApiClient } from "../api/client";
import { ClaimConflict } from "../api/client";
import { usePolling } from "./usePolling";
export function QueueScreen({ client, onOpen }: { client: ApiClient; onOpen: (id: string) => void }) {
  const { data, refresh } = usePolling(() => client.getQueue(), 5000);
  const [msg, setMsg] = useState<string | null>(null);
  const rows = (data ?? []).slice().sort((a, b) => b.score - a.score);
  async function claim(id: string) {
    setMsg(null);
    try { await client.claim(id); onOpen(id); refresh(); }
    catch (e) { setMsg(e instanceof ClaimConflict ? "already claimed by another agent" : "claim failed"); }
  }
  return (
    <section aria-label="case queue">
      {msg && <p role="alert" style={{ color: "var(--danger)" }}>{msg}</p>}
      <table><thead><tr><th>Case</th><th>Score</th><th>Risk</th><th>Intent</th><th>SLA</th><th></th></tr></thead>
        <tbody>
          {rows.map((it) => (
            <tr key={it.conversation_id}>
              <td>{it.conversation_id}</td><td>{it.score.toFixed(1)}</td><td>{`R${it.risk_class}`}</td><td>{it.intent ?? "—"}</td>
              <td>{it.sla.breached
                ? <span style={{ color: "var(--danger)", fontWeight: 600 }}>⚠ breach</span>
                : it.sla.defined ? `${Math.round(it.sla.remaining_secs / 60)}m` : "—"}</td>
              <td><button onClick={() => claim(it.conversation_id)} aria-label={`claim ${it.conversation_id}`}>Claim</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
