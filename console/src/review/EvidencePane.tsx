import type { ReviewSurface } from "../api/types";
export function EvidencePane({ surface }: { surface: ReviewSurface }) {
  const { booking, autonomy, evidence } = surface;
  return (
    <aside aria-label="evidence" style={{ overflow: "auto", display: "grid", gap: "var(--pane-gap)" }}>
      <section aria-label="autonomy">
        <h3>Autonomy{autonomy.outcome ? ` — ${autonomy.outcome}` : ""}{autonomy.route ? ` → ${autonomy.route}` : ""}{autonomy.confidence_band ? ` · ${autonomy.confidence_band}` : ""}</h3>
        {autonomy.auto_send_eligible
          ? <p>Auto-send eligible.</p>
          : <ul>{(autonomy.reasons_for_agent ?? []).map((r, i) => <li key={i}>{r}</li>)}</ul>}
      </section>
      <section aria-label="booking">
        <h3>Booking</h3>
        {booking.available
          ? <dl>{([["ref", booking.ref], ["status", booking.status], ["destination", booking.destination], ["dates", booking.dates?.join(" – ")], ["payment", booking.payment_status], ["balance", booking.balance_due], ["accommodation", booking.accommodation], ["transport", booking.transport]] as const)
              .filter(([, v]) => v).map(([k, v]) => <div key={k}><dt style={{ color: "var(--muted)" }}>{k}</dt><dd>{v}</dd></div>)}</dl>
          : <p style={{ color: "var(--muted)" }}>booking data unavailable{booking.reason ? ` (${booking.reason})` : ""}</p>}
        {booking.available && booking.withheld && <p style={{ color: "var(--warn)" }}>Some details withheld pending verification.</p>}
      </section>
      <section aria-label="sources">
        <h3>Cited sources</h3>
        {evidence.length === 0 ? <p style={{ color: "var(--muted)" }}>none</p>
          : <ul>{evidence.map((s) => <li key={s.id}>{s.title ?? s.id}{s.url ? ` — ${s.url}` : ""}</li>)}</ul>}
      </section>
    </aside>
  );
}
