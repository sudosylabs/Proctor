# Hosted authentication boundary contract

`PublicAccessDiscovery.ts` is the single browser authority for fetching and
runtime-validating `GET /api/v1/discovery`. It accepts only HTTP 200, discovery
version 1, the complete capability and Desktop-compatibility shapes, bounded
provider descriptors, and an optional complete public Institution
presentation. It parses `canonical_origin` once and requires its origin to
equal the origin serving the hosted page.

The module exposes only three outcomes: validated same-origin discovery,
origin mismatch, or safe unavailability. Malformed bodies, invalid origins,
unexpected statuses, and transport failures are unavailable; arbitrary server
values never reach presentation.

The shared boundary does not decide feature admission or visible recovery.
Login decides whether any admitted method exists, registration decides whether
public registration is enabled, Invitation acceptance treats Institution
presentation as optional, and Desktop authorization combines the validated
Institution with its authenticated User check. Tests use an in-memory
transport behind the same loader interface as the production HTTP adapter.

Fragment extraction, CSRF behavior, and navigation remain separately owned by
their narrow modules in this directory.
