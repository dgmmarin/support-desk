import { render, screen } from "@testing-library/react";
import { EvidencePane } from "./EvidencePane";
import type { ReviewSurface } from "../api/types";
const surface: ReviewSurface = {
  conversation_id: "c1", customer_message: { from: "cust@x", direction: "inbound", body: "When do I depart?", automated: false }, thread: [],
  draft_available: true, draft: "You depart 09:00.", inline_citations: [{ claim_span: "You depart 09:00.", booking_field_path: "itinerary.departure", resolved: true }],
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
