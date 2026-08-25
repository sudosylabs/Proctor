---
name: execution-environments
description: Change Execution Profiles, images, host placement, grants, isolation, workspace projection, Attempt Terminals, PTY transport, capacity, revocation, or the execenv boundary.
---

# Change execution environments

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) first. Completion: Execution
   Environment, Attempt Workspace, Terminal, Profile, Image, and host remain
   distinct.
2. Read the relevant section of the
   [execution-environment reference](references/execution.md). Completion:
   product policy, exam-blind host mechanics, trust boundary, topology,
   lifetime, and projection direction are explicit.
3. Keep authoritative workspace and grant state in Proctor; keep isolation,
   readiness, ensure/revoke, projection, PTY, and capacity mechanics in
   `execenv`. Completion: neither side imports or reconstructs the other's
   policy.
4. Fence every host action with the current grant and participation authority;
   treat the host as replaceable and non-authoritative. Completion: stale or
   revoked hosts cannot mutate or expose current Attempt state.
5. Update contract, projection, PTY, TLS, capacity, revocation, and failure
   tests across both boundaries as applicable. Completion: repair and replay
   converge without promoting host-local state.
