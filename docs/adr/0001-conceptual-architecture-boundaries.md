---
status: accepted
---

# Use conceptual architecture boundaries

Proctor treats transport, application, domain, and persistence as
responsibility and dependency boundaries rather than requiring matching
top-level directory trees. This preserves cohesive, discoverable packages such
as `app/api` for HTTP, `websocket` for its stateful wire protocol, `app`,
`model`, `store`, and `store/sqlstore`, while retaining clear dependency
direction and avoiding mechanical fragmentation of each feature across
layer-named packages.
