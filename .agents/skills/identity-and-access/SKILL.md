---
name: identity-and-access
description: Change Proctor bootstrap, Access Policy, hosted authentication, Invitations, desktop authorization, credentials, Sessions, MFA, external identity, account lifecycle, or onboarding batches.
---

# Change identity and access

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) first. Completion: User,
   Principal, Session, credential, Invitation, affiliation, membership, and
   Role Binding remain distinct.
2. Choose the branch reference:
   - bootstrap, Access Policy, hosted browser flows, desktop handoff,
     Invitations, or administrative batches:
     [access and onboarding](references/access-and-onboarding.md);
   - User identity, credentials, Sessions, MFA, account lifecycle, CAS, or
     OIDC: [identity and authentication](references/identity.md).
   Completion: proof, state, expiry, assurance, and admission ownership are
   explicit.
3. Establish an immutable Principal at the transport boundary; authorize the
   exact action and resource inside the owning use case. Completion: no
   credential or membership snapshot silently grants later permissions.
4. Use purpose-specific Store facts and named terminal operations. Completion:
   plaintext proofs, credentials, tokens, and provider assertions are bounded,
   destroyed or rotated at their owning transition, and absent from logs.
5. Update rate-limit, replay, revocation, assurance, audit, provider, Store,
   transport, and integration tests affected by the flow. Completion: failure
   behavior remains indistinguishable where account or policy disclosure would
   be unsafe.
6. Run the focused identity/access phase target and
   `make -C server architecture`. Completion: browser, desktop, API, mail, and
   persistence surfaces agree.

## Current open decisions

- Choose the next provider family after CAS and OIDC: SAML/RENATER or LDAP.
- Define any provider-directory synchronization for profile fields,
  affiliations, reconciliation, and deprovisioning without granting interactive
  sign-in or explicit linking general directory authority.
