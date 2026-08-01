# HTTP contract conventions

[`../../openapi.json`](../../openapi.json) is the reviewed public HTTP contract.
Its coverage began with the migrated Academic Unit reference slice and now
expands with each migrated capability without weakening existing contracts.

Use the Academic Unit slice as the conceptual pattern for later capabilities:

- define request and response DTOs in the owning transport file;
- map DTOs explicitly to application commands and results;
- register every route with one authentication requirement and repeat that
  value in OpenAPI's `x-proctor-auth` extension;
- use closed request and response schemas unless a named extension object is
  intentionally open;
- document stable application error codes in `x-proctor-error-codes`, map each
  code to a declared HTTP response, and return RFC 9457 Problem Details;
- add an agreement test that compares registered route/auth metadata, DTO JSON
  fields, success schemas, and public errors with OpenAPI;
- preserve characterized v1 behavior. Contract changes are additive unless a
  new API version and migration path are introduced.

Two Academic Unit shapes are frozen compatibility exceptions, not target
patterns:

- its v1 PATCH DTO uses pointers, so omitted and explicit `null` currently have
  the same meaning. Later slices must use the architecture's `Optional[T]`
  representation when those states differ; do not copy the pointer shape;
- its v1 collection response is a bare JSON array. New collection contracts
  use an object with non-null `items` and, where applicable, `next_cursor`.

The agreement test records these exceptions so migration cannot silently
change existing clients. It does not make them conventions for new endpoints.

Correct transport ownership:

```go
type createAcademicUnitRequest struct {
    Name        string `json:"name"`
    DisplayName string `json:"display_name"`
}

unit, err := academicUnits.CreateAcademicUnit(ctx, invocation, command)
writeJSON(writer, http.StatusCreated, academicUnitResponseFromModel(unit))
```

Incorrect domain serialization and transport policy:

```go
var unit model.AcademicUnit
decodeJSON(writer, request, &unit, "update")
store.AcademicUnit().Update(request.Context(), &unit)
```

The OpenAPI document describes the wire contract only. It does not generate or
dictate domain models, application commands, persistence rows, or handlers.
