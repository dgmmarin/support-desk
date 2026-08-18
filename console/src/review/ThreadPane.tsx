import type { Message } from "../api/types";
function Msg({ m }: { m: Message }) {
  return (
    <article style={{ borderBottom: "1px solid var(--line)", padding: 8 }}>
      <div style={{ color: "var(--muted)", fontSize: 12 }}>{m.from} · {m.direction}{m.automated ? " · auto" : ""}</div>
      {m.subject && <div style={{ fontWeight: 600 }}>{m.subject}</div>}
      <div>{m.body}</div>
    </article>
  );
}
export function ThreadPane({ customerMessage, thread }: { customerMessage: Message; thread: Message[] }) {
  return (
    <div aria-label="customer thread" style={{ overflow: "auto" }}>
      <Msg m={customerMessage} />
      {thread.map((m, i) => <Msg key={i} m={m} />)}
    </div>
  );
}
