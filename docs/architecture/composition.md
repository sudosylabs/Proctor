# Composition & Lifecycle

## Composition and lifecycle

`server.New` is the sole composition root. It loads configuration, constructs concrete adapters and store layers, gives infrastructure to `platform.Service` for lifecycle ownership, translates configuration into narrow application policies, constructs `app.App`, constructs HTTP and WebSocket transports, and wires post-commit effects. Construction is inert; blocking `Server.Run` alone enters runtime lifecycle stages and returns only after shutdown or runtime failure.

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

Startup enters Platform, commits the PostgreSQL serving-node lease, reconciles
protected Role and pending offline-recovery state, then starts durable Jobs,
WebSocket, listener ownership, and HTTP serving before publishing readiness.
Server records those stages privately: cancellation or failure before HTTP
accepts the listener closes the Server-owned listener. Normal shutdown makes a
running node unready and drains HTTP before Jobs, WebSocket, the HTTP transport,
the exact serving lease, and Platform are disposed. A serving-lease renewal
failure instead makes the node unready and force-stops HTTP before its last
lease can expire. That failed lease remains until PostgreSQL expiry so offline
recovery cannot begin early; exact-incarnation withdrawal applies only to
normal shutdown. Every constructed owner is disposed exactly once, including on
close-before-start; only successfully entered active stages require runtime
drain behavior. Shutdown uses bounded deadlines and retains drain and cleanup
failures for concurrent or repeated callers. Liveness, readiness, and
authorized dependency diagnostics remain distinct signals.

After internal readiness is published, `Server.Run` invokes at most one
optional, promptly returning host-process readiness observer. The observer has
no infrastructure lifecycle and cannot make an otherwise ready node fail; its
errors are operational diagnostics. The CLI uses this seam to emit systemd's
`READY=1` datagram without making the module-root server depend on environment
variables or a supervisor-specific implementation.

## Module placement

- Keep Proctor domains in one `server` module — identity, authorization, academics, exams, WebSocket, clustering stay in `server`; extract `packages/*` only with a Proctor-independent contract and external consumers.
- Keep platform out of application services — `app` depends on ports and `model`/`store`, never on `platform.Service`.
