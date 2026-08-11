# Composition & Lifecycle

## Composition and lifecycle

`server.New` is the sole composition root. It loads configuration, constructs concrete adapters and store layers, gives infrastructure to `platform.Service` for lifecycle ownership, translates configuration into narrow application policies, constructs `app.App`, constructs HTTP and WebSocket transports, wires post-commit effects, and starts components in dependency order.

Construction may be split across cohesive files in the module-root `server`
package. That keeps backend selection reviewable without turning `server.New`
into one undifferentiated function or creating a second composition root.

`platform.Service` owns shared infrastructure health, reconfiguration, startup, and shutdown. It is retained by `server.Server`, never passed into `app`, and never used as a service locator.

After the root has selected VFS and transferred its lifecycle to
`platform.Service`, it explicitly constructs the stateless File Content module
over that VFS and projects only the application's bounded content capabilities.
File Content neither selects a backend nor participates in lifecycle shutdown.

Constructors are inert. They validate required dependencies but do not normally start listeners or goroutines. Explicit `Start` methods begin work; `Close` or `Shutdown` is idempotent and bounded. Partial failure unwinds resources already acquired.

Dependency injection is manual. Reflection containers, generated DI containers, global registries, and global mutable state are prohibited.

Application services do not receive `config.Config` or `config.Store`. Composition supplies small immutable policies, such as `SessionPolicy`, or narrow providers for explicitly dynamic behavior.

Startup constructs configuration, concrete adapters, schema validation,
platform lifecycle ownership, application services, transports, and workers in
dependency order; listeners become ready last. Shutdown reverses that order
under bounded deadlines. Liveness, readiness, and authorized dependency
diagnostics remain distinct signals.

## Module placement

- Keep Proctor domains in one `server` module — identity, authorization, academics, exams, WebSocket, clustering stay in `server`; extract `packages/*` only with a Proctor-independent contract and external consumers.
- Keep platform out of application services — `app` depends on ports and `model`/`store`, never on `platform.Service`.
