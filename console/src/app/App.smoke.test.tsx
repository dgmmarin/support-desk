import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "./App";
import type { ReviewSurface } from "../api/types";
test("with a session, shows the queue and opens a case", async () => {
  const surface: ReviewSurface = { conversation_id: "c1", customer_message: { from: "c", direction: "inbound", body: "hi", automated: false }, thread: [],
    draft_available: true, draft: "Hi there.", inline_citations: [], evidence: [], booking: { available: false, withheld: false },
    autonomy: { present: true, auto_send_eligible: false }, translation: { original_message: "", draft: "", mt_available: false } };
  const client = {
    getQueue: async () => [{ conversation_id: "c1", score: 5, risk_class: 0, enqueued_at: "", sla: { defined: false, remaining_secs: 0, breached: false }, locked: false, status: "pending", queue: "normal" }],
    claim: async () => ({ expires_at: "" }), resolve: async () => ({ resolved: true }),
    getReview: async () => surface, act: async () => ({ action: "approve_send", sent: true }),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any;
  sessionStorage.setItem("td.session", JSON.stringify({ tenantId: "t1", token: "jwt" }));
  render(<App client={client} />);
  await userEvent.click(await screen.findByRole("button", { name: /claim c1/i }));
  expect(await screen.findByLabelText("draft reply")).toBeInTheDocument();
});
