# Execution environments

This document owns the accepted Execution Environment design. It is a
contract, not an implementation claim; current capability status lives in
[Project status](../project/status.md). Canonical terms are defined in
[`CONTEXT.md`](../../CONTEXT.md).

## Scope and ownership

An Execution Environment is the isolated, non-authoritative projection of one
Attempt Workspace in which a candidate may use one Attempt Terminal. It is
not a second workspace, not a local folder, and not an academic grader.

The server owns product meaning: the Execution Profile, when a grant may
exist, placement across hosts, Attempt Workspace acknowledgement, the
Attempt Terminal bridge on the Attempt Connection, pause and revocation, and
audit. The reusable `packages/guest` module owns the exam-blind host
contract: readiness, ensure and revoke, tree projection, one PTY, freeze,
capacity, and typed errors. The Execution Host binary lives in a separate
repository, implements the server side of that contract, and owns isolation
machinery. Reusable modules never import the Proctor server.

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
rootfs blobs, kernels, or Dockerfiles, and they do not install packages into
a running guest. The selected image is the guest rootfs and must already be
present on any host that may receive the grant.

Network modes are `none` (default) and `allowlist`. Allowlist destinations
are installation-defined on the host, not arbitrary creator URLs. Creator-
chosen open internet is out of the initial contract.

## Isolation

Production isolation is Firecracker. Warmup and readiness fail closed when
usable KVM is absent. There is no container fallback in the initial
contract. The in-memory `packages/guest` adapter exists for tests and must
not be selected for production.

The microVM is the escape boundary. The guest rootfs is visible and
read-only so the toolchain can live there. The Attempt Workspace is the only
writable tree, mounted at a fixed path that is the candidate's working
directory and home. `cd` into image paths is allowed. There is no parent
that contains other exams or host state. A shell hook is not the security
boundary.

## Client module

`packages/guest` talks to exactly one host. Placement across hosts stays in
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

The host repository imports these types and serves the same contract. The
client does not expose a hypervisor control surface.

## Topology

Only the installation talks to hosts. The exam client talks only to Proctor.

An operator installs the host binary on a supported Linux KVM machine, gives
it the installation host token and API address, and the host registers. It
advertises health, remaining capacity, and present images. Creators never
name hosts. Candidates never see them.

At first terminal open the server places the Attempt onto one host that has
the profile's image and enough capacity, then pins that choice. If the
pinned host is dead on reconnect, the server places again and rebuilds the
guest from the Attempt Workspace. Unacknowledged guest bytes do not move
with the Attempt.

## Lifetime

Admission does not boot a guest. An Execution Environment is created on the
first authorized Attempt Terminal open and only when the frozen profile
enables one. Pause freezes the environment without destroying it. Submit
and sitting close revoke it after the durable Attempt state commits.
Confirmed connection loss revokes after a short grace. Reconnect after
freeze or grace always replaces the tree from acknowledged workspace state
before attaching a PTY.

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

A default ignore set excludes dependency and build trees such as
`node_modules`, `target`, `__pycache__`, and `.git`. Ignored, over-quota, or
invalid writes stay on the ephemeral guest disk and never enter the
Submission. Non-ignored writes that cannot be acknowledged must surface an
error in the terminal. Paths outside the workspace mount, including `/tmp`,
are ephemeral.

## Attempt Terminal transport

PTY bytes are not workspace mutations. They travel student to Proctor on
the existing Attempt Connection, then Proctor to the host through
`packages/guest`. The reverse path is the same. Pause, lease expiry, kick,
and manager-cannot-see-live-work apply because the Attempt Connection
already owns those gates.

PTY octets, tree bodies, grant tokens, and workspace paths never enter
ordinary logs or unsafe audit fields. Initial integrity evidence continues
to exclude terminal output and source code.

## Resources

CPU, memory, disk, and process caps are installation-defined defaults and
maxima. The Execution Profile does not request hardware. The host
advertises remaining slots and memory. The server refuses `Ensure` when no
host fits. The host reports capacity; it does not pick winners.

## Deferred

Implementation of this contract, the host binary, the exam-client terminal
surface, exact default resource numbers, an authored ignore list, an
installation image registry, and an installation-gated open-internet mode
remain later slices. Multiple PTYs, in-guest exec besides the one shell,
and guest-disk snapshots are not in the initial interface.

## Rationale

The Attempt Workspace already exists as the remotely authoritative exam
work. Treating the guest as a second authority would split submission,
pause, and reconnect. A hypervisor-shaped client would push Firecracker
into every exam use case. An exam-aware host would split the examination
module across a network and could not live in `packages/`. A student-direct
or relay topology would create a second enforcement plane. Firecracker
without a supported-host profile would promise an iroh-style empty-VPS
install that KVM cannot keep. The independent `packages/guest` contract is
the extractable seam `dependencies.md` requires; isolation stays in the
host repository because it has no Proctor-independent callers inside this
monorepo.
