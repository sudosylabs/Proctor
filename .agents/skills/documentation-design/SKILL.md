---
name: documentation-design
description: Create, reorganize, or review Proctor's agent instructions, skills, component contracts, READMEs, or documentation authorities. Use when deciding where durable engineering knowledge belongs.
---

# Design repository knowledge

## Workflow

1. Classify every proposed statement by authority. Completion: each statement
   has exactly one destination from the placement table below.
2. Inspect the destination's consumers, generators, nearest `AGENTS.md`, and
   existing exact contracts. Completion: the change cannot leave two live
   authorities or a stale inbound link.
3. Write for the task that invokes the knowledge. Completion: an agent can tell
   when to load it, what to do, and how to know the task is finished without
   reading an inventory document.
4. Update source, consumers, generated views, and validation in one slice.
   Completion: the superseded authority is removed rather than redirected or
   archived.
5. Run the narrow generators and checks plus `make -C server architecture`.
   Completion: new failures are resolved and any pre-existing failure is
   reported separately.

## Placement

| Knowledge | Authority |
| --- | --- |
| Universal repository behavior | Root [`AGENTS.md`](../../../AGENTS.md) |
| Subtree-only behavior | Nearest concise `AGENTS.md` |
| Repeatable task workflow | Task-specific `.agents/skills/<name>/SKILL.md` |
| Conditional detail for one workflow | The owning skill's `references/` |
| Canonical domain vocabulary | [`glossary`](../glossary/SKILL.md) |
| Exact component behavior | Contract or README beside the component |
| Public task guidance | `docs/public/` or `docs/api/` |
| Public API shapes | `server/openapi/` and generated `server/openapi.json` |
| Site implementation | `docs/site/` |
| Discoverable implementation detail | Code, tests, configuration, or command help |
| Completed chronology | Git history |
| Licensing and adaptation provenance | `LICENSING.md` and the applicable notice |

Keep root and nested `AGENTS.md` files as routing and universal guardrails, not
skill catalogs. A skill description names distinct positive trigger branches;
its body starts with actions and checkable completion criteria. Split
branch-specific reference when it would obscure the main workflow. Keep the
main `SKILL.md` below 500 lines and normally below 200.

Public pages never link to internal skills. Generated output names its tracked
input in source comments when maintainers need that provenance, but public
reader copy does not expose the agent-authoring structure.

Repository-relative Markdown links target content tracked in the same change.
Use HTTPS for external sources. Machine paths, ignored material, local scratch
notes, compatibility copies, tracked migration logs, and discoverable tree
inventories are not authorities.
