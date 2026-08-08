---
status: accepted
---

# Separate HTTP DTOs from domain models

Each HTTP transport area owns its request and response DTOs and explicitly maps
them to application commands, queries, and results. Mutable domain entities do
not double as JSON wire formats, although stable domain identifiers and enums
may be reused when deliberately included in the public contract. This differs
from Mattermost's frequent direct model serialization and prevents domain or
persistence evolution from silently changing the API or exposing sensitive
fields.
