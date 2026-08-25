---
name: authorization-audit
description: Add or change Proctor actions, resources, roles, scoped bindings, authorization checks, assurance requirements, security-sensitive audit, redaction, logging, or privacy safeguards.
---

# Change authorization and security policy

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) first. Completion: the Principal,
   Action, Resource, Role, scope, and domain relationship have exact meanings.
2. Read [authorization and audit](references/authorization.md) for permission,
   scope, enforcement, or decision-audit work. Read
   [security and privacy](references/security.md) for credential accounting,
   examination containment, redaction, logging, or observability work.
   Completion: the exact trust boundary and failure disclosure are explicit.
3. Resolve current authoritative resource state in the owning application use
   case immediately before work or mutation. Completion: transport proof,
   affiliation, membership, cache state, or stale role snapshots cannot grant
   authority.
4. Commit security-sensitive mutations and required allow/deny audit according
   to the named aggregate contract. Completion: the operation fails closed when
   its required audit cannot be made durable.
5. Keep credentials, secrets, student data, Exam answers, private rationale,
   file/mail content, and unbounded input out of ordinary logs and unsafe audit
   fields. Completion: safe projections and bounded identifiers are the only
   observable payloads.
6. Add permission-matrix, concealment, scope-inheritance, assurance, replay,
   redaction, and audit-failure tests, then run `make -C server architecture`.
   Completion: denied and unavailable paths reveal no more than the contract.
