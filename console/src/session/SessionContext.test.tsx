import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionProvider, useSession } from "./SessionContext";

function Probe() {
  const { session, setSession } = useSession();
  return (
    <div>
      <span data-testid="tenant">{session?.tenantId ?? "none"}</span>
      <button onClick={() => setSession({ tenantId: "t1", token: "jwt" })}>
        set
      </button>
    </div>
  );
}

test("stores and exposes the session", async () => {
  render(
    <SessionProvider>
      <Probe />
    </SessionProvider>
  );
  expect(screen.getByTestId("tenant")).toHaveTextContent("none");
  await userEvent.click(screen.getByText("set"));
  expect(screen.getByTestId("tenant")).toHaveTextContent("t1");
  expect(JSON.parse(sessionStorage.getItem("td.session")!).tenantId).toBe("t1");
});
