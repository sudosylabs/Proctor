---
name: upstream-adaptation
description: Evaluate, copy, or substantially adapt upstream source, behavior, documentation, or assets into Proctor. Use for Mattermost-derived work and any change that creates provenance or notice obligations.
---

# Adapt upstream work

## Workflow

1. Identify the exact upstream repository, revision, path, and governing
   license before copying. Completion: the source is reproducible and its terms
   are compatible with the destination described in
   [`LICENSING.md`](../../../LICENSING.md).
2. Distinguish behavioral reference from direct or substantial adaptation.
   Completion: copied expression is not mislabeled as independent work.
3. Rework the material through Proctor's domain, architecture, security, and
   accessibility boundaries. Completion: upstream structure does not become an
   accidental Proctor authority.
4. Record required provenance and notices in the same slice; server
   adaptations update [`server/NOTICE`](../../../server/NOTICE). Completion:
   the exact revision, path, license, and nature of the adaptation are durable.
5. Run the destination's focused tests and review the diff for copied secrets,
   branding, fixtures, generated noise, and incompatible license text.
   Completion: the adaptation is attributable, scoped, and verified.

Mattermost's open-source repository is an eligible behavior and source
reference. Source Available or commercial Mattermost material requires explicit
permission. Local reference checkouts are optional evidence, never tracked authority.
