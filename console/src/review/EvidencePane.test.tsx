import { render, screen, within } from "@testing-library/react";
import { EvidencePane } from "./EvidencePane";
import type { ReviewSurface } from "../api/types";
const surface: ReviewSurface = {
  conversation_id: "c1", customer_message: { from: "cust@x", direction: "inbound", body: "When do I depart?", automated: false }, thread: [],
  draft_available: true, draft: "You depart 09:00.", inline_citations: [
    { claim_span: "You depart 09:00.", booking_field_path: "itinerary.departure", resolved: true },
    { claim_span: "Refunds take 5 days.", knowledge_item_id: "k1", resolved: false },
  ],
  evidence: [{ id: "k1", title: "FAQ" }],
  booking: { available: false, withheld: false, reason: "connector down" },
  autonomy: { present: true, outcome: "human_review", route: "review", auto_send_eligible: false, reasons_for_agent: ["G05 confidence below threshold"] },
  translation: { original_message: "", draft: "", mt_available: false },
};
test("shows degraded booking and the autonomy reasons", () => {
  render(<EvidencePane surface={surface} />);
  expect(screen.getByText(/booking data unavailable/i)).toBeInTheDocument();
  expect(screen.getByText(/G05 confidence below threshold/)).toBeInTheDocument();
});
test("renders autonomy outcome and route", () => {
  render(<EvidencePane surface={surface} />);
  const autonomyHeading = screen.getByRole("heading", { name: /Autonomy/ });
  expect(autonomyHeading.textContent).toContain("human_review");
  expect(autonomyHeading.textContent).toContain("review");
});
test("renders a source list", () => {
  render(<EvidencePane surface={surface} />);
  expect(screen.getByText("FAQ")).toBeInTheDocument();
});
test("renders inline citations as a claim to source list, flagging unresolved ones", () => {
  render(<EvidencePane surface={surface} />);
  const citations = screen.getByRole("region", { name: /citations/i });
  expect(within(citations).getByText(/You depart 09:00\./)).toBeInTheDocument();
  expect(within(citations).getByText(/itinerary\.departure/)).toBeInTheDocument();
  expect(within(citations).getByText(/Refunds take 5 days\./)).toBeInTheDocument();
  expect(within(citations).getByText(/unresolved/i)).toBeInTheDocument();
});
