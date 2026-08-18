import { render, screen } from "@testing-library/react";
import { DraftEditor } from "./DraftEditor";

test("warns when there are unsupported claims", () => {
  render(<DraftEditor value="Hello." onChange={() => {}} unsupported={["Hello."]} />);
  expect(screen.getByText(/unsupported claim/i)).toBeInTheDocument();
});
