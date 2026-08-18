import type { ReviewSurface } from "../api/types";
export function EvidencePane({ surface }: { surface: ReviewSurface }) {
  const { booking, autonomy, evidence, inline_citations } = surface;
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
      {/* ponytail: plain claim -> source list, not in-textarea span highlighting —
          the draft is a plain <textarea> (DraftEditor), which has no concept of
          rich spans/ranges to anchor a highlight to. Wiring hover-highlight back
          into the draft text needs a rich-text/contenteditable editor, which is a
          later slice; this list is the agreed fidelity for now. */}
      <section aria-label="citations">
        <h3>Citations</h3>
        {inline_citations.length === 0 ? <p style={{ color: "var(--muted)" }}>none</p>
          : <ul>{inline_citations.map((c, i) => {
              const target = c.knowledge_item_id ?? c.booking_field_path ?? "—";
              return (
                <li key={i}>
                  “{c.claim_span}” → {target}
                  {!c.resolved && <span style={{ color: "var(--warn)" }}> ⚠ unresolved</span>}
                </li>
              );
            })}</ul>}
      </section>
    </aside>
  );
}
