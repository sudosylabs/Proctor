# Desktop release agreement verification

This package verifies immutable release artifacts selected by `server.New`.
The application receives validated model values. Public requests, Institution
preferences and Desktop uploads cannot introduce release keys, catalog entries,
schema bytes, verification claims or supported-format lists.

An expectation pins the build, exact target, application release identity,
registry, source manifest, every source/effect schema, signed capability matrix,
detector catalog and Attempt Configuration manifest. The matrix carries
`registry_digest`, `matrix_id`, `release_id`, `target_tuple` and one entry per
admitted registry coverage key in sorted order. Each entry binds the exact
`source_schema_digest`, claim, component, adapter, optional helper, permissions,
limitations and verification result. Numeric schema-version selectors from
older matrices are rejected.

The verifier checks an Ed25519 signature over the exact matrix payload before
interpreting it. Matrix and detector documents use the
[canonical JSON contract](../internal/canonicaljson/README.md), reject unknown,
case-aliased, duplicate or omitted fields and cannot exceed their bounds. Raw
packaged registry, source manifest and schema files retain their exact byte
identities; they are not reconstructed or normalized before hashing. Compiled
coverage definitions are the reviewed interpretation of that exact registry.
The release key and pinned artifacts must be admitted together; a signature by
an arbitrary uploaded key is insufficient.

The current detector interpretation is a finite catalog of exact condition IDs,
versions, capabilities, source requirements and modes. It accepts no executable
pattern or native detail bag. Packaged Candidate-safe command/keybinding
catalogs belong to the configuration manifest; identifiers are not executable
commands and key chords are not keybinding identifiers.

An enforce claim requires passed certification. Mandatory display, window and
Proctor-owned protection baseline sources require enforce certification even
when every optional family is disabled. Observe-only requirements can truthfully
return unavailable coverage; unavailable sources must not be represented as
opened/working sources by admission. Effect-only claims remain requirements
without opening an observation source. Source health and readiness are runtime
facts checked separately by preflight, not facts inferred from a signature.

The production release catalog and admitted key set remain empty until real
signed target artifacts and certification evidence are supplied. Synthetic test
fixtures verify rejection behavior without claiming that any production target
has passed native certification. No runtime or test reads an adjacent checkout.
