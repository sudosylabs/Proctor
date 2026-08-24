# Execution environments

This document owns the accepted Execution Environment design and records the
implemented server/execenv boundary. Current capability status lives in
[Project status](../project/status.md). Canonical terms are defined in
[`CONTEXT.md`](../../CONTEXT.md).

## Scope and ownership

An Execution Environment is the isolated, non-authoritative projection of one
Attempt Workspace in which a candidate may use one Attempt Terminal. It is
not a second workspace, not a local folder, and not an academic grader.

The server owns product meaning: the Execution Profile, when a grant may
exist, placement across hosts, Attempt Workspace acknowledgement, the
Attempt Terminal bridge on the Attempt Connection, pause and revocation, and
audit. The reusable module
[`github.com/sudosylabs/execenv`](https://github.com/sudosylabs/execenv)
owns the exam-blind host contract: readiness, ensure and revoke, tree
projection, one PTY, freeze, capacity, and typed errors. The Execution Host
binary lives in that same repository, serves the contract, and owns
isolation machinery. This monorepo requires execenv; it does not contain
it. Reusable modules never import the Proctor server.

Students never learn host addresses. Hosts never receive Attempt, Sitting,
Participation, Session, or general VFS credentials.

## Authoring

An Execution Profile is authored on the Exam Draft and frozen into the Exam
Revision beside the Starter Workspace. It is not part of the Exam Policy Set
and is not a `devcontainer.json` or Dockerfile. Live correction cannot change
it. A new revision may change it for later sittings; an open sitting keeps
the profile it started with.

The profile chooses whether the exam offers an Attempt Terminal, which
catalog Execution Image to use, and which network mode applies. The default
is off: a published exam without an enabled profile must not grow a
terminal, and the host is never contacted. Enabling the terminal is an
authored choice, not an installation default.

Creators pick an image from the installation catalog. They do not upload
kernels or disks, and they do not install packages into a running guest.
An Execution Image is a baked Firecracker disk (kernel + root filesystem)
already on the host, not a Docker tag. The default published disk is a
broad toolchain image in the class of GitHub Codespaces' universal
devcontainer, produced by a bake step that also installs the execenv guest
agent. Operators may bake further catalog ids. The daemon never pulls at
grant time.

The authoring API exposes the deduplicated image ids and network modes from
currently usable isolated configured hosts. It never exposes host ids,
addresses, credentials, release details, or live capacity.

Network modes are `none` (default) and `allowlist`. Allowlist destinations
are installation-defined on the host, not arbitrary creator URLs. Creator-
chosen open internet is out of the initial contract.

## Isolation

Production isolation is Firecracker. Warmup and readiness fail closed when
usable KVM is absent. There is no container fallback in the initial
contract. The in-memory execenv adapter exists for tests and must
not be selected for production.

The microVM is the escape boundary. The guest rootfs is visible and
read-only so the toolchain can live there. The Attempt Workspace is the only
writable tree, mounted at a fixed path that is the candidate's working
directory and home. `cd` into image paths is allowed. There is no parent
that contains other exams or host state. A shell hook is not the security
boundary.

## Client module

execenv talks to exactly one host. Placement across hosts stays in
the server. The application programs a small `Host` / `Env` interface:

- `Ready` reports whether the host is usable, which image ids it has, and
  remaining capacity.
- `Ensure` creates or reattaches a grant. The grant id is allocated and
  stored by the server; it is not an Attempt id. The same id with a
  different image or network is a conflict.
- `ReplaceTree` pushes a full snapshot. Unchanged path-and-version pairs
  skip bodies.
- `Apply` pushes incremental IDE-originated mutations.
- `Watch` and `Open` harvest guest-originated create, replace, move, and
  delete events. Ingest is first-class.
- `Attach` yields exactly one PTY. A second attach is busy until that PTY
  closes.
- `Freeze` and `Thaw` stop and resume I/O without destroying the grant.
- `Revoke` destroys the grant. It is idempotent.

Adapters implement an internal transport that factors control, PTY, and tree
so a dropped console is hangup rather than a dead grant. The public
interface does not name Firecracker, jailer, vsock, or KVM. Required
adapters are `memory` and `remote`, plus a conformance suite. Production
composition refuses a non-isolated adapter.

The execenv daemon serves the same contract the Proctor client dials. The
interface does not expose a hypervisor control surface.

## Topology

Only the installation talks to hosts. The exam client talks only to Proctor.

An operator installs the host binary on a supported Linux KVM machine and
configures each Proctor node with the host's stable operator ID, address, and
TLS/mTLS or token credentials. Hosts never call Proctor and there is no host
registration endpoint. Proctor makes outbound authenticated connections and
reads live health, remaining capacity, present images, networks, and release
compatibility from execenv. Every Proctor node in one installation must use
the same stable host-ID catalog. Creators never name hosts. Candidates never
see them.

At first terminal open the server places the Attempt onto one host that has
the profile's image and enough capacity, then pins that choice. If the
pinned host is dead on reconnect, the server places again and rebuilds the
guest from the Attempt Workspace. Unacknowledged guest bytes do not move
with the Attempt.

PostgreSQL stores the chosen grant ID, stable host ID, image, network, state,
revision, and release/revocation progress. It deliberately does not index live
host readiness or capacity: those observations expire with the connection and
are recomputed from the bounded configured host set. Reassignment releases the
old placement and reserves its replacement atomically before either transient
host effect runs. A partial unique index permits only one active placement per
Attempt while retaining placement history.

## Lifetime

Admission does not boot a guest. An Execution Environment is created on the
first authorized Attempt Terminal open and only when the frozen profile
enables one. Pause freezes the environment without destroying it, and Resume
thaws that same environment. Submit and sitting close revoke after the durable
Attempt state commits. Confirmed connection loss revokes after a short grace.
Reconnect after freeze or grace always replaces the tree from acknowledged
workspace state before attaching a PTY.

Terminal open resolves the protected Attempt presentation before beginning the
critical audit, then ensures placement, starts Workspace observation, attaches
the PTY, and completes the audit. Every failure during that open transaction,
after placement and before ownership is returned, closes acquired observation
or terminal resources and retries release of the exact grant until its durable
fence succeeds, even when the request context has already ended. After return,
observation loss or an unacknowledgeable event closes the caller-owned terminal and
releases that exact grant, forcing the next authorized open to build a fresh
projection from durable Workspace state. A normal caller close does not revoke
the placement; the Attempt lifecycle remains the authority for when that grant
may otherwise exist. This bridge does not create an independent terminal
lifecycle.

## Projection

The Attempt Workspace remains the durable authority. The Execution
Environment is a synchronized projection. Losing a client, node, or
environment cannot discard an acknowledged change.

The IDE and the Attempt Terminal are dual writers. Authoritative create,
replace, move, and delete still commit through the existing workspace
protocol. After acknowledgement the server applies the change to the guest.
Guest writes under the workspace mount become workspace mutations only after
the server harvests them through `Watch` and `Open` and they pass the same
acknowledgement rules, quotas, path contract, and reserved `.proctor` root.

Every Workspace mutation carries a closed origin: `candidate` or
`execution_host`. Both origins commit through the same Attempt service and
publish the same safe realtime result. Only candidate-originated changes are
applied to the host; execution-host changes are already present there and must
not echo through `Apply`. Mutation provenance is never inferred from context.

Observation loss closes the terminal and releases its exact execution grant.
The host API cannot atomically reset the projection and install a replacement
watch, so in-place recovery could miss writes from an already-running guest
process. A later authorized open receives a fresh environment constructed from
durable Workspace state. Harvesting reads files through a bounded stream,
enforces the Workspace per-file limit before mutation, and uses the host event
cursor to derive deterministic retry keys. An unacknowledgeable non-ignored
event fails the terminal rather than silently claiming durability.

An asynchronous failure fences PTY writes and terminal close, durably releases
the exact grant, and only then wakes the transport reader. The native PTY closes
immediately; transient durable-release failures retry with bounded backoff while
the reader and caller-facing close remain fenced. This ordering keeps the
connection's terminal slot occupied until a reopen is guaranteed to select a
successor grant rather than exposing the still-current placement.

A default ignore set excludes dependency and build trees such as
`node_modules`, `target`, `__pycache__`, and `.git`. Ignored, over-quota, or
invalid writes stay on the ephemeral guest disk and never enter the
Submission. A move wholly within ignored trees remains ignored. A file move
across the boundary is acknowledged as an authoritative delete when its
destination is ignored, or an authoritative create when its source is ignored.
Directory moves across the boundary fail the terminal because the host event
cannot enumerate an ignored subtree and the durable Store rejects deleting a
non-empty directory; partially projecting such a move is forbidden. The
isolated execenv v0.2 watcher also expands a directory rename into unordered
per-path deletes followed by creates, with no atomic batch boundary. The bridge
therefore rejects a directory create, a directory or descendant delete, and a
create whose parent tree is not already authoritative before committing any
member. An adapter that supplies one atomic `Move` event can still acknowledge
that directory move. Until the reusable host contract exposes batch topology,
students create and remove directory trees through the authoritative Workspace
protocol rather than the terminal shell. A kind-changing event that presents an
authoritative directory as a host file is likewise rejected before mutation;
delete-then-create is not an atomic repair.
Non-ignored writes that cannot be acknowledged must surface an error in the
terminal. Paths outside the workspace mount, including `/tmp`, are ephemeral.

## Attempt Terminal transport

PTY bytes are not workspace mutations. They travel student to Proctor on
the existing Attempt Connection, then Proctor to the host through execenv.
The reverse path is the same. Pause, lease expiry, kick,
and manager-cannot-see-live-work apply because the Attempt Connection
already owns those gates.

PTY octets, tree bodies, grant tokens, and workspace paths never enter
ordinary logs or unsafe audit fields. Initial integrity evidence continues
to exclude terminal output and source code.

## Resources

CPU, memory, disk, and process caps are installation-defined defaults and
maxima. The Execution Profile does not request hardware. execenv v0.2 reports
remaining slots; each host enforces its configured memory and other resource
caps and returns typed capacity refusal from `Ensure`. The server tries only
hosts advertising a free slot and re-places on a typed capacity refusal. The
host reports or enforces capacity; it does not pick winners. Advertising
remaining memory separately is deferred until the reusable execenv contract
exposes it.

## Implemented boundary

The server integration is implemented against execenv v0.2.0: typed multi-host
deployment configuration and secret redaction, TLS 1.3/mTLS or loopback-only
development dialing, connection recovery, fail-closed readiness, deterministic
capability/capacity placement, durable assignment and cleanup history,
authoritative PostgreSQL/VFS workspace projection, acknowledged IDE-change
resynchronization, submission revocation, and bounded desired-state
reconciliation that repairs missed open/pause/resume/release effects from
authoritative Attempt and Sitting state as well as pending revocations. Each
grant records the applied Sitting lifecycle state and revision; PostgreSQL
conditionally accepts an effect acknowledgement only while that exact state
and revision remain current. A PostgreSQL advisory lease serializes host
lifecycle effects for the exact grant across application nodes; after acquiring
it, the worker rereads authoritative Attempt and Sitting state instead of using
its triggering snapshot. The worker validates that same dedicated connection
before preparing and immediately after every host effect. Preparation persists
a pending state/revision before Freeze or Thaw; completion clears it atomically
with the applied marker. Connection loss releases/revokes the exact grant, and
process loss leaves the pending marker for the next lease holder to release,
so an orphaned host request cannot be mistaken for convergence,
persisted Draft authoring and immutable Revision freezing,
authorized Attempt WebSocket terminal open/input/resize/close with non-replayable
bounded output, guest-write acknowledgement through the existing Workspace
commands, durable candidate terminal-open audit, audit-correlated lifecycle
release, pause/resume/release hooks, and periodic cleanup. The independent
execenv repository supplies the host binary,
remote protocol, in-memory adapter, and conformance suite.

Exact default resource numbers, an authored ignore list, and an operator image
catalog UI remain later product slices. The initial server ignore set is fixed
to `.proctor`, `.git`, `node_modules`, `target`, and `__pycache__`. Multiple PTYs, in-guest exec
besides the one shell, and guest-disk snapshots are not in the initial
interface.

## Rationale

The Attempt Workspace already exists as the remotely authoritative exam
work. Treating the guest as a second authority would split submission,
pause, and reconnect. A hypervisor-shaped client would push Firecracker
into every exam use case. An exam-aware host would split the examination
module across a network and could not live in `packages/`. A student-direct
or relay topology would create a second enforcement plane. Firecracker
without a supported-host profile would promise an iroh-style empty-VPS
install that KVM cannot keep. The independent execenv contract is the extractable
seam `dependencies.md` requires; isolation stays in that repository
because it has no callers inside this monorepo.
