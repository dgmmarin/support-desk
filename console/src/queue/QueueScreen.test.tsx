import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { QueueScreen } from "./QueueScreen";
import { ClaimConflict } from "../api/client";
import type { QueueItem } from "../api/types";
const items: QueueItem[] = [
  { conversation_id: "c1", score: 9, risk_class: 1, intent: "faq", channel: "email", enqueued_at: "", sla: { defined: true, remaining_secs: -60, window_secs: 3600, breached: true }, locked: false, status: "pending", queue: "normal" },
  { conversation_id: "c2", score: 3, risk_class: 0, intent: "faq", channel: "email", enqueued_at: "", sla: { defined: true, remaining_secs: 1800, window_secs: 3600, breached: false }, locked: false, status: "pending", queue: "normal" },
];
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function client(overrides = {}): any {
  return { getQueue: async () => items, claim: async () => ({ expires_at: "" }), resolve: async () => ({ resolved: true }), getReview: async () => ({}), act: async () => ({}), ...overrides };
}
test("renders rows ordered by score with a breach badge", async () => {
  render(<QueueScreen client={client()} onOpen={() => {}} />);
  const rows = await screen.findAllByRole("row");
  expect(within(rows[1]).getByText("c1")).toBeInTheDocument();
  expect(within(rows[1]).getByText(/breach/i)).toBeInTheDocument();
});
test("claim conflict shows a message and does not open", async () => {
  const onOpen = vi.fn();
  const c = client({ claim: async () => { throw new ClaimConflict(409, "x"); } });
  render(<QueueScreen client={c} onOpen={onOpen} />);
  await userEvent.click(await screen.findByRole("button", { name: /claim c1/i }));
  expect(await screen.findByText(/already claimed/i)).toBeInTheDocument();
  expect(onOpen).not.toHaveBeenCalled();
});
