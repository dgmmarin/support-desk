import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { ReviewScreen } from "./ReviewScreen";
import { PermissionError } from "../api/client";
import type { ReviewSurface } from "../api/types";
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function client(surface: ReviewSurface, overrides = {}): any { return { getReview: async () => surface, act: async () => ({ action: "approve_send" }), ...overrides }; }
const draftSurface: ReviewSurface = {
  conversation_id: "c1", customer_message: { from: "c", direction: "inbound", body: "hi", automated: false }, thread: [],
  draft_available: true, draft: "Hello there.",
  inline_citations: [], evidence: [], booking: { available: false, withheld: false },
  autonomy: { present: false, auto_send_eligible: false }, translation: { original_message: "", draft: "", mt_available: false },
};
test("shows the draft_status when there is no draft", async () => {
  const s: ReviewSurface = { conversation_id: "c1", customer_message: { from: "c", direction: "inbound", body: "hi", automated: false }, thread: [],
    draft_available: false, draft: "", draft_status: "abstained_or_escalated",
    inline_citations: [], evidence: [], booking: { available: false, withheld: false }, autonomy: { present: false, auto_send_eligible: false }, translation: { original_message: "", draft: "", mt_available: false } };
  render(<ReviewScreen client={client(s)} conversationId="c1" agent="me" onDone={() => {}} />);
  expect(await screen.findByText(/abstained_or_escalated/)).toBeInTheDocument();
});
test("shows a Back to queue button when the case fails to load, and it calls onDone", async () => {
  const onDone = vi.fn();
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const c = { getReview: async () => { throw new Error("boom"); } } as any;
  render(<ReviewScreen client={c} conversationId="c1" agent="me" onDone={onDone} />);
  const back = await screen.findByRole("button", { name: /back to queue/i });
  await userEvent.click(back);
  expect(onDone).toHaveBeenCalled();
});
test("shows a Back to queue button while loading", () => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const c = { getReview: () => new Promise(() => {}) } as any;
  render(<ReviewScreen client={c} conversationId="c1" agent="me" onDone={() => {}} />);
  expect(screen.getByRole("button", { name: /back to queue/i })).toBeInTheDocument();
});
test("permission error on act shows insufficient role, distinct from generic failure", async () => {
  const c = client(draftSurface, { act: async () => { throw new PermissionError(403, "insufficient role"); } });
  render(<ReviewScreen client={c} conversationId="c1" agent="me" onDone={() => {}} />);
  await userEvent.click(await screen.findByRole("button", { name: /approve & send/i }));
  expect(await screen.findByText(/insufficient role/i)).toBeInTheDocument();
});
test("edit_send with an empty draft shows a message and does not call act", async () => {
  const act = vi.fn(async () => ({ action: "edit_send" }));
  const c = client(draftSurface, { act });
  render(<ReviewScreen client={c} conversationId="c1" agent="me" onDone={() => {}} />);
  const textarea = await screen.findByLabelText("draft reply");
  await userEvent.clear(textarea);
  await userEvent.click(screen.getByRole("button", { name: /edit & send/i }));
  expect(await screen.findByText(/draft is empty/i)).toBeInTheDocument();
  expect(act).not.toHaveBeenCalled();
});
