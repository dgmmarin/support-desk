import { render, screen } from "@testing-library/react";
import { ReviewScreen } from "./ReviewScreen";
import type { ReviewSurface } from "../api/types";
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function client(surface: ReviewSurface) { return { getReview: async () => surface, act: async () => ({ action: "approve_send" }) } as any; }
test("shows the draft_status when there is no draft", async () => {
  const s: ReviewSurface = { conversation_id: "c1", customer_message: { from: "c", direction: "inbound", body: "hi", automated: false }, thread: [],
    draft_available: false, draft: "", draft_status: "abstained_or_escalated",
    inline_citations: [], evidence: [], booking: { available: false, withheld: false }, autonomy: { present: false, auto_send_eligible: false }, translation: { original_message: "", draft: "", mt_available: false } };
  render(<ReviewScreen client={client(s)} conversationId="c1" agent="me" onDone={() => {}} />);
  expect(await screen.findByText(/abstained_or_escalated/)).toBeInTheDocument();
});
