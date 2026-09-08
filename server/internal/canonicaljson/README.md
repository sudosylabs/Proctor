# Canonical Desktop agreement bytes

This internal standard-library-only package owns the integer-only JSON byte
representation shared by Desktop agreement documents. Its consumers own closed
schemas, semantic bounds, authenticity, and which fields are excluded from a
particular digest. It does not decode portable User Settings source or change
the established general application-command idempotency representation.

`Canonicalize` validates the original input before `encoding/json` can replace
invalid Unicode or erase number spelling. It accepts one value with ASCII object
keys, sorts keys recursively, preserves array order and Unicode scalar sequences,
and serializes strings exactly like `JSON.stringify`. HTML-sensitive characters
and U+2028/U+2029 stay literal UTF-8. Neither Go HTML-escaping options nor
`strconv.Quote` alone provide the agreed bytes.

Duplicate decoded keys, invalid UTF-8, unpaired surrogate escapes, non-ASCII keys,
fractional/exponent number spellings, unsafe integers, trailing values and malformed
JSON fail. The encoding layer permits signed safe integers; a document's SafeInt
or PositiveInt field validator enforces its narrower domain. Negative zero becomes
zero where the field allows it. Required/unknown/null fields remain schema-owned.
The caller supplies its input/output byte limit; nesting is additionally bounded
to 64 containers. Errors contain no document data.

Policy model encoders first validate their typed values, encode their selected
semantic fields, then canonicalize before storage/digest. A JSON marshal roundtrip
is not a substitute for validating raw input: it can already have erased malformed
strings, duplicates, or integer spelling. Validate the original bytes first.

The portable [agreement vectors](testdata/agreement.json) preserve exact input and
expected bytes in base64, including invalid UTF-8, and pin expected SHA-256 hashes.
They are suitable for both server and Desktop tests. Check them with:

```sh
go test ./internal/canonicaljson
node internal/canonicaljson/verify-fixtures.mjs
```

Run commands from the server module. The Node reference checks valid values against
JavaScript string serialization; Go unit tests also exercise all rejected inputs.
The Node script is a development check, not a server/runtime/test prerequisite.
These vectors establish encoding parity, not signed release or OS certification.
