---
name: server-boundaries
description: Design or change Proctor server packages, dependency direction, application services, composition, lifecycle ownership, interfaces, or module extraction. Use when deciding where server behavior belongs or adding an architectural seam.
---

# Change server boundaries

## Workflow

1. Identify the behavior owner, consumers, dependencies, and exclusions before
   adding a package or interface. Completion: the proposed seam represents a
   stable responsibility rather than symmetry, file size, or speculative reuse.
2. Read the exact branch reference:
   - package imports or extraction: [dependency reference](references/dependencies.md);
   - use cases, application children, interfaces, or facade construction:
     [application reference](references/application.md);
   - root wiring, startup ownership, readiness, or disposal:
     [composition reference](references/composition.md).
   Completion: every affected production dependency is accounted for.
3. Keep domain and application policy independent of HTTP, WebSocket, SQL,
   Redis, SMTP, VFS, Memberlist, execution hosts, and concrete adapters.
   Completion: infrastructure points inward through a consumer-owned port or
   the deliberate bounded Store contracts.
4. Introduce the boundary with a working vertical slice. Completion: package
   comments name ownership, exclusions, and allowed dependencies; no empty
   placeholder or alternate composition path remains.
5. Update the ordered import policy and focused tests, then run
   `make -C server architecture`. Completion: every production package matches
   an explicit rule and every forbidden edge fails the gate.

The module-root `server.New` is the sole composition root. `platform.Service`
owns infrastructure lifecycle and health and never becomes an application
service locator. `app.New` is the sole application constructor; `app.App` is a
use-case facade, not a Store accessor. Concrete backend selection remains in
the root server package.

Use direct typed commands, queries, and results. `app.Invocation` carries the
immutable principal and safe call metadata; `context.Context` carries
cancellation and deadlines, not hidden security state. Actor-sensitive use
cases authorize against current resource state immediately before expensive
work or mutation. Background work enters through application use cases rather
than manipulating stores directly.

Interfaces live beside their consumer and expose one cohesive need. The
bounded root `store.Store` and its model/aggregate contracts are the deliberate
grouped exception for shared conformance. A child package cannot import its
parent, select infrastructure, or redeclare a sibling's contracts.
