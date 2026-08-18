import { render, screen } from "@testing-library/react";
import { App } from "./App";
test("renders the console shell heading", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: /tourdesk console/i })).toBeInTheDocument();
});
