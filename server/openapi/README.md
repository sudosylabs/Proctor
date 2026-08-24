# OpenAPI authoring

This directory is the human authoring interface for Proctor's public API
contract. [`../openapi.json`](../openapi.json) is the deterministic, reviewed
artifact consumed by agreement tests, documentation tooling, and API clients;
it is generated and must not be edited by hand.

## Find the right module

[`base.yaml`](base.yaml) owns document metadata, servers, and the stable public
tag taxonomy. [`fragments/`](fragments/) is organized first by public product
area and then by the resource or workflow named in the route:

```text
fragments/
  authentication/
    auth-login.yaml
    users-mfa.yaml
    shared.yaml
  examinations/
    exams.yaml
    exams-draft-resources.yaml
    exams-revisions.yaml
    shared.yaml
  shared.yaml
```

A resource module contains its `paths` and any parameters, request bodies,
responses, or schemas used only by those paths. A product area's `shared.yaml`
contains definitions reused by several resources in that area. The root
`shared.yaml` contains definitions used across product areas or by the whole
document.

Find an existing route or operation directly instead of reading an inventory:

```sh
rg -n '/api/v1/exams|operationId: createExam' server/openapi/fragments
```

## Add or change an operation

1. Choose the existing product-area directory and the narrowest resource
   module. Create another descriptive `.yaml` file when no existing module is
   a natural owner; there is no manifest to update.
2. Add the complete OpenAPI path item. Every operation has exactly one tag from
   `base.yaml`, a summary, a substantive behavioral `description`, explicit
   `security`, `x-proctor-auth`, `x-proctor-error-codes`, and
   `x-proctor-idempotency`.
3. Describe every parameter and the purpose of every request body. Every
   mutation needs a schema-valid synthetic media example or a reviewed
   executable-style `x-codeSamples` example; bodyless and multipart mutations
   use `x-codeSamples`. Never use real Institution, student, Exam, answer,
   credential, object-key, email, or local-machine data.
4. Co-locate definitions used only by that resource. Promote a definition to
   the area's `shared.yaml` or the root `shared.yaml` only when a second real
   consumer needs it.
5. Regenerate and run agreement checks:

   ```sh
   make -C server openapi-build
   make -C server openapi-agreement
   ```

The build recursively discovers YAML files under `fragments/`; lexical file
order affects only deterministic assembly, never ownership. Each fragment is a
normal partial OpenAPI document with only `paths` and/or `components` at its
root. Duplicate paths and component names fail compilation, as do unresolved
references, invalid OpenAPI, duplicate operation IDs, undeclared tags, and
missing Proctor operation metadata.

The documentation audit is the editorial complement to the compiler. From
`docs/site`, run `npm run audit:openapi`; it enforces complete behavior,
parameter, request-body, mutation-example, and representative response-example
coverage across the compiled contract. Schema validation proves JSON media
examples match their referenced request or response schemas.

## Build boundary

The compiler's interface is intentionally small: it accepts a source
filesystem, discovers and merges fragments, validates the assembled OpenAPI
3.1 document, and emits deterministic JSON. The repository command provides
the filesystem and artifact adapters:

```sh
# Regenerate server/openapi.json after an intentional source change.
make -C server openapi-build

# Validate YAML and prove the tracked artifact has no drift.
make -C server openapi-check
```

CI runs the drift check before HTTP/OpenAPI agreement. Documentation tooling
reads the generated JSON artifact; it never needs to understand the fragment
layout or maintain another contract copy.
