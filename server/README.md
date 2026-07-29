# Proctor server

This directory contains the new Proctor application server. It is an
independent Go module and is licensed under AGPL-3.0-only.

The server currently establishes one cohesive construction flow:

```text
config.Store → platform.Service → app.Server/app.App → app/api
```

`app.NewServer` is the sole composition root. The shared `testlib` constructs
that same graph with an in-memory config store and captured logs.

The flat `model` package now establishes the durable model contract:

- Mattermost-inspired 26-character IDs and millisecond timestamps;
- `PreSave`, `PreUpdate`, and `IsValid` lifecycle methods;
- safe `Auditable` representations;
- translation-ID-based `AppError` values mapped to HTTP Problem Details;
- institution, hierarchical academic unit, programme, programme level,
  academic period, and class models;
- user profiles separated from external identities and local password
  credentials;
- time-bounded affiliations, academic-unit memberships, and class memberships;
- roles and scoped role bindings;
- sessions separated from hashed access/refresh credentials, plus scoped
  expiring personal access tokens;
- hashed, expiring, single-use password-reset and email-verification tokens.

Identity services, authentication middleware, authorization evaluation, exams,
persistence, and WebSockets remain intentionally unimplemented until their
next vertical slices.

## Run locally

From the repository root:

```sh
go run ./server/cmd/proctor serve --config ./server/config.example.json
```

The default listener is `127.0.0.1:8065`. Available endpoints are:

- `GET /health/live`
- `GET /health/ready`
- `GET /api/v1/system/version`

Validate a configuration without starting the server:

```sh
go run ./server/cmd/proctor config validate --config ./server/config.example.json
```

Configuration is loaded in this order: built-in defaults, an optional strict
JSON file, then `PROCTOR_` environment variables. Unknown JSON fields and
invalid values are rejected at startup.

The active configuration is owned by one concurrency-safe store. It separates
persisted values from environment overrides, returns cloned snapshots, supports
atomic file writes, reload/set listeners, and structured diffs. Logging is the
first dynamically reconfigurable consumer; HTTP listener and timeout changes
require a process restart.

Logging supports multiple independently filtered console or file targets,
text/JSON formatting, contextual fields, bounded field sizes, runtime
reconfiguration, flush/shutdown, and locked test capture.

## Verify

```sh
make -C server check
```

The individual `test`, `test-race`, `vet`, and `build` targets run against the
standalone server module as well.
