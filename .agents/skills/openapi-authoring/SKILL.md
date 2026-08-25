---
name: openapi-authoring
description: Add or change Proctor HTTP operations, schemas, examples, product-area metadata, or generated API reference content. Use when editing server/openapi sources or synchronizing the public API contract.
---

# Author the OpenAPI contract

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) when the operation introduces or
   changes domain language. Read the HTTP
   [component contract](../../../server/httpapi/CONTRACT.md) and the
   [OpenAPI authoring guide](../../../server/openapi/README.md). Completion:
   runtime behavior, public shape, and terminology have identified owners.
2. Edit the human-owned YAML module closest to the product area or resource.
   Completion: paths and co-located definitions have one source; genuinely
   shared definitions remain in the applicable `shared.yaml`.
3. Supply the complete operation contract required by the authoring guide,
   including explicit Proctor authentication, error, and idempotency metadata
   plus synthetic examples. Completion: no sensitive or local data appears in
   prose or examples.
4. Run `make -C server openapi-build` and review the generated
   `server/openapi.json` diff. Completion: the deterministic artifact expresses
   only the intended contract change.
5. Run `make -C server openapi-check` and the relevant HTTP agreement tests.
   For reference presentation changes, also run the OpenAPI audit and renderer
   checks from `docs/site`. Completion: source, runtime agreement, artifact,
   and rendered reference agree.

Never edit generated `server/openapi.json` or `docs/api/reference/` output by
hand. The generator owns discovery, collision checks, reference resolution,
schema validation, and deterministic serialization. Docusaurus adapters may
present the contract but never rewrite route or authorization meaning.

## Current open decision

Decide whether generated client SDKs belong in this monorepo and which desktop
languages are required before adding an SDK generation or publication surface.
