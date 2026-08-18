import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ActionBar } from "./ActionBar";
test("pressing 'a' fires approve_send", async () => {
  const onAct = vi.fn();
  render(<ActionBar onAct={onAct} busy={false} />);
  await userEvent.keyboard("a");
  expect(onAct).toHaveBeenCalledWith("approve_send");
});
test("clicking Escalate fires escalate", async () => {
  const onAct = vi.fn();
  render(<ActionBar onAct={onAct} busy={false} />);
  await userEvent.click(screen.getByRole("button", { name: /escalate/i }));
  expect(onAct).toHaveBeenCalledWith("escalate");
});
