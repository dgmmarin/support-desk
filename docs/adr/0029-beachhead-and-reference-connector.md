# 0029 — Nordic/Romanian beachhead; Tourpaq-profile reference connector

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner)
- **PRD source:** OD-02, OD-17, §5.3, FR-M12-03

## Context

OD-02 (beachhead market) and OD-17 (reference reservation connector) are linked: the first market drives
language priorities, legal review and — critically — which reservation back-end the reference connector
targets. The PRD notes a Nordic/Romanian operator profile (Tourpaq context) as the likely starting point.

## Decision (provisional)

**Beachhead:** an EU/EEA operator with a **Nordic/Romanian** profile (per the Tourpaq context in OD-02),
setting first-market languages and legal review scope. **Reference connector:** target that first design
partner's back-end (the Tourpaq profile) as the reference implementation of the Reservation Connector
Interface ([ADR-0009](0009-reservation-connector-interface.md)), alongside the generic CSV/SFTP/DB-view and
file-drop connectors for operators without an API (FR-M12-03).

## Alternatives considered

- **Broad multi-market launch** — rejected: dilutes language quality (RSK-14) and legal focus; a beachhead
  is the disciplined choice.
- **Build a generic connector first, no reference** — rejected: a real reference connector against a real
  partner's system is what proves the interface and Phase 4 value.

## Consequences

- Depends entirely on securing operator #1 as design partner (OD-03) and confirming whether their back-end
  exposes an API or requires a DB/export integration (OD-17).
- Sets per-language eval sets and native-speaker review at onboarding (RSK-14).
- If the partner is not Tourpaq-profile, this ADR is superseded — the *interface* is unaffected, only the
  reference adapter.

## Sign-off needed

Confirm the beachhead market/languages and the specific reference back-end once operator #1 (OD-03) is
signed and OD-17 (API vs export) is answered.
