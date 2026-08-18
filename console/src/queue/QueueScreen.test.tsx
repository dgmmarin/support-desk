import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { QueueScreen } from "./QueueScreen";
import { ClaimConflict, PermissionError, MissingTenant } from "../api/client";
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
  await screen.findByText("c1");            // wait for the poll to populate data rows
  const rows = screen.getAllByRole("row");  // header + 2 data rows now present
  expect(within(rows[1]).getByText("c1")).toBeInTheDocument();   // highest score first
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
test("claim permission error shows an insufficient role message", async () => {
  const onOpen = vi.fn();
  const c = client({ claim: async () => { throw new PermissionError(403, "insufficient role"); } });
  render(<QueueScreen client={c} onOpen={onOpen} />);
  await userEvent.click(await screen.findByRole("button", { name: /claim c1/i }));
  expect(await screen.findByText(/insufficient role/i)).toBeInTheDocument();
  expect(onOpen).not.toHaveBeenCalled();
});
test("poll error shows an alert instead of a blank table", async () => {
  const c = client({ getQueue: async () => { throw new Error("boom"); } });
  render(<QueueScreen client={c} onOpen={() => {}} />);
  expect(await screen.findByRole("alert")).toHaveTextContent(/could not load queue/i);
});
test("poll permission error shows insufficient role", async () => {
  const c = client({ getQueue: async () => { throw new PermissionError(403, "x"); } });
  render(<QueueScreen client={c} onOpen={() => {}} />);
  expect(await screen.findByRole("alert")).toHaveTextContent(/insufficient role/i);
});
test("poll missing tenant error shows check tenant/token", async () => {
  const c = client({ getQueue: async () => { throw new MissingTenant(400, "x"); } });
  render(<QueueScreen client={c} onOpen={() => {}} />);
  expect(await screen.findByRole("alert")).toHaveTextContent(/check tenant\/token/i);
});
test("locked row shows locked-by badge and disables claim", async () => {
  const c = client({ getQueue: async () => [
    { conversation_id: "c3", score: 5, risk_class: 0, enqueued_at: "", sla: { defined: false, remaining_secs: 0, breached: false }, locked: true, claimed_by: "agent-x", status: "pending", queue: "normal" },
  ] });
  render(<QueueScreen client={c} onOpen={() => {}} />);
  expect(await screen.findByText(/locked by agent-x/i)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /claim c3/i })).not.toBeInTheDocument();
});
