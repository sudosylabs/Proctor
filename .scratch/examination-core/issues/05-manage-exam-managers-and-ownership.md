# 05 — Manage Exam Managers and ownership

**What to build:** Allow authorized Exam Managers to grant and revoke equal
management relationships and transfer ownership without weakening the invariant
that every Exam always has one eligible owner who is also a Manager.

**Blocked by:** 01 — Create and retrieve an Exam Draft with safe shipped defaults.

**Status:** completed

- [x] Manager listing uses bounded keyset pagination ordered by grant time and
      user ID and returns grant actor/time plus creator/owner indicators without
      hydrating profiles or unrelated user data.
- [x] Add Manager checks the target is an active eligible user with the required
      current academic relationship and revalidates that fact inside the named
      atomic operation against races.
- [x] Duplicate addition, missing removal, ineligible target, and revision
      conflicts map to stable safe failures rather than generic persistence
      errors.
- [x] The current owner cannot be removed and at least one Manager always exists;
      immutable creator provenance grants no authority after relationship or
      permission loss.
- [x] Ownership transfer changes ownership only: the target must already be an
      eligible Manager, the prior owner remains a Manager, and same-owner
      transfer is rejected as a no-op.
- [x] Manager and ownership mutations require expected Exam revision, current
      Manager relationship plus role permission, audit, and command idempotency.
- [x] System-administrator override is an explicit authorization path and never
      creates hidden membership or bypasses owner/eligibility invariants.
- [x] Named Store operations lock and atomically enforce Manager uniqueness,
      owner membership, eligibility, Exam revision, audit success, and outcome.
- [x] Safe effects publish only Exam/User identifiers, relationship state,
      revision, and time after commit; private profile or authored data is absent.
- [x] Store conformance, PostgreSQL race cases, authorization revocation,
      pagination, HTTP/OpenAPI, audit, realtime, race, and architecture tests pass.
