import { useEffect } from "react";
import type { ActionKind } from "../api/types";

const KEYS: Record<string, ActionKind> = { a: "approve_send", e: "edit_send", x: "escalate", r: "reject" };
const LABEL: Record<ActionKind, string> = { approve_send: "Approve & send (a)", edit_send: "Edit & send (e)", escalate: "Escalate (x)", reject: "Reject (r)" };

export function ActionBar({ onAct, busy }: { onAct: (a: ActionKind) => void; busy: boolean }) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "TEXTAREA" || tag === "INPUT") return;
      const a = KEYS[e.key.toLowerCase()];
      if (a && !busy) { e.preventDefault(); onAct(a); }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onAct, busy]);

  return (
    <div role="toolbar" aria-label="case actions" style={{ display: "flex", gap: 8, borderTop: "1px solid var(--line)", padding: 8 }}>
      {(["approve_send", "edit_send", "escalate", "reject"] as ActionKind[]).map((a) => (
        <button key={a} disabled={busy} onClick={() => onAct(a)}>{LABEL[a]}</button>
      ))}
    </div>
  );
}
