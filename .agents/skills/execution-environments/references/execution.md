# Execution-environment reference

This reference owns the accepted Execution Environment design and the
server/execenv boundary. Canonical terms are defined in the repository
[`glossary` skill](../../glossary/SKILL.md); code and tests expose current
implementation state.

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
- `InitializeProjection` installs a fresh snapshot; `ApplyProjection` advances
  the consecutive durable journal through immutable, bounded content transfers.
- `Observe` and `OpenObservationContent` retain semantic guest effects and their
  original bytes. Confirmation binds an accepted outcome before acknowledgement
  releases retained content.
- `Attach` yields exactly one PTY. A second attach is busy until that PTY
  closes.
- `Control` requests ordered freeze, resume or revoke and confirms the actual
  effect under the current fence; freeze/resume preserves the same guest and PTY.
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
Reopening on a retained, fenced journal environment reconciles its existing
grant and projection before attaching. It never resets a live guest's tree.
A missing or invalid epoch requires exact-grant retirement and a fresh projection
from acknowledged Workspace state; legacy grants are also retired before reopening.

Required corrections selecting `terminal` fence placement/input through their
current Attempt-owned acknowledgement state and use the existing protective
grant release. Browser-only corrections do not release unrelated grants.
This correction path currently rebuilds after release; it is not evidence of
reversible guest freeze or continuation of the same running process.

Terminal open resolves the protected Attempt presentation before beginning the
critical audit, then ensures placement, starts Workspace observation, attaches
the PTY, and completes the audit. Projection catch-up and current interaction denial
close any acquired observation or terminal resources while retaining a healthy
fenced grant for recovery. Other failures after placement release the exact grant;
release retries until its durable fence succeeds, even after request cancellation. After return,
observation loss or an unacknowledgeable event closes the caller-owned terminal and
releases that exact grant, forcing the next authorized open to build a fresh
projection from durable Workspace state. Temporary interaction denial instead
retains the original terminal identity, PTY and unacknowledged semantic capture.
Input and resize return a failure without a closed frame; observation processing
retries after current authority permits it. Immutable captures keep their original
fence while content access, confirmation and acknowledgement use the current
running fence of the same grant/epoch. Capture revisions cannot exceed it.
A normal caller close does not revoke
the placement; the Attempt lifecycle remains the authority for when that grant
may otherwise exist. This bridge does not create an independent terminal
lifecycle.

## Fenced control

The consumer-owned controlled-environment extension binds an opaque host epoch
once to a reserved Execution Grant. PostgreSQL owns an independent monotonic
control revision, requested state and acknowledged revision. Preparation commits
before host I/O. Identical retries keep the same intent; a replacement epoch
requires a new grant. An acknowledgement must match the exact grant, epoch,
revision and requested state, and the original current-authority digest. The
private digest binds lifecycle, effective native security transitions,
Participation, credential, correction and deadline gates. Native receipt and
watermark churn alone never changes execution authority. The security owner
retains the processed control sequence of its latest effective gate transition,
so a fault followed by recovery still supersedes the earlier acknowledgement even
when no host worker saw the intermediate fault. It is never transmitted to the host.

Native coverage and host acknowledgement are separate gates. Healthy native
coverage may permit recovery, but execution input waits for confirmed running
state under the current authority. A healthy/fault/healthy sequence cannot reuse
an earlier running acknowledgement. A failed or lost host response remains
unconfirmed. Reconciliation retries the durable intent; an untrustworthy receipt,
lost occupancy or lost lifecycle lease uses exact-grant retirement. Successful
freeze/recovery preserves the same supported host occupancy.

Committed native control updates and lease-renewal retries trigger bounded
reconciliation. Their durable command outcome survives transient effect failure;
periodic reconciliation repairs a missed callback. Security projections distinguish
freeze_pending/frozen and thaw_pending/ready from actual acknowledgements.
Browser-only corrections do not enter the terminal gate.

The execenv v0.3.1 adapter selects this extension only when the authenticated
host advertises ordered control, journal projection and semantic observations,
and the native environment supplies a valid opaque epoch. Unsupported hosts retain
protective refusals. Protocol and persistence tests do not certify Linux/KVM
containment; production use requires certification of the installed artifacts.

## Projection

The Attempt Workspace remains the durable authority. The Execution
Environment projects acknowledged state. Losing a client, node, or environment
cannot discard an acknowledged change. Each grant records its applied Workspace
cursor and any pending projection cursor. Projection preparation commits before
host I/O. The fenced projection port retains one exact bounded request per grant,
including its mutation identity, original fence, journal prefix, host sequence,
and immutable upload receipts. Completion requires the matching host receipt;
later unrelated Workspace commits do not invalidate an acknowledged prefix.
Completed effects discard private request bodies and retain bounded minimal
receipts for exact retries. A released grant discards unfinished private requests.
Reconciliation compares durable Workspace progress independently of control.

The private journal reader preserves the original object reference and expected
file version at each position; it never reads a newer live file as an earlier
save. Pages contain at most 128 consecutive changes and 256 KiB of metadata.
Atomic initialization carries the complete tree at its actual Workspace cursor
under a separate 4 MiB metadata ceiling, including JSON-escaped paths. It never
splits a snapshot by inventing journal positions.
A missing journal position or missing pinned body returns no partial batch.
Obsolete objects needed by a bound ready grant's unapplied retained journal
prefix remain protected from cleanup. Grant release or confirmed advancement
removes that extra protection; ordinary durable references still apply.

Application adapters exposing ProjectionEnvironment use exact pending-request
recovery and consecutive journal projection, including when reusing an existing
epoch. Frozen control performs no projection I/O. Unknown transport outcomes
retain the pending request for retry; an invalid receipt, lost epoch or unusable
content handle requires exact-grant retirement. Host cursor conflicts require
observation reconciliation before further projection. Normal builds select the
released journal adapter only when the authenticated host advertises the required
capabilities. Unsupported hosts remain unavailable.

A definitive host validation refusal has its own durable outcome: retain the
request digest as rejected, clear its pending marker, and leave the applied
cursor unchanged. A later request uses a new mutation identity after observation
reconciliation. A timeout cannot be recorded as a refusal, and neither successful
nor refused identities can be reused with changed bytes.

The semantic observation path preserves the original fence, host sequence,
projected cursor, stable node identity, expected version and captured-content
receipt. Its resolver revalidates current candidate access/control and checks the
retained Workspace journal for competing path/topology edits. A subsequent edit
of the same guest node may extend that exact node's previous accepted outcome;
it cannot adopt the newest competing Workspace version as its baseline. Unknown
identity, a journal/sequence gap, or an unsupported boundary change fails closed.

The existing five Workspace operations atomically commit the authoritative
change, accepted observation outcome, host-node/Entry binding and processed host
sequence. Directory moves preserve descendant bindings; recursive deletion
removes the subtree and tombstones those bindings. Captured content is checked
against staged immutable bytes. Semantic harvesting requires its original proof;
a bound grant cannot fall back to unfenced legacy harvesting. Outcomes and
bindings are bounded to 65,536 accepted host sequences per grant, after which a
fresh authorized grant is required; release drops these private records.

Events wholly within `.git`, `node_modules`, `target` or `__pycache__` advance a
durable ignored outcome and the host watermark without changing the Workspace.
Cross-boundary moves are refused before partial mutation. `.proctor` remains
reserved. Never claim a Workspace commit or send a projection confirmation for
an ignored event. An unsolicited claimed projection echo is rejected by the
current host path, whose projector emits no such observation; exact accepted guest
echoes are instead bound by its explicit confirmation protocol.

The application retains the lifecycle lease through semantic acceptance and host
confirmation, reads only captured event content, confirms the exact durable
Workspace outcome, and only then acknowledges release of retained host bytes.
The legacy post-commit execution callback is skipped for these semantic results
so it cannot project a guest mutation before confirmation. The concrete semantic
adapter and terminal readiness path use the released execenv dependency in the
normal server build, subject to authenticated host capability checks.

The IDE and the Attempt Terminal are dual writers. Authoritative create,
replace, move, and delete still commit through the existing workspace
protocol. Initial projection reserves the grant before capturing the Workspace
snapshot. Incremental projection reads only the changed file body; it never
reloads all Workspace file bodies for each save. Fenced projection and semantic
observations preserve the same guest and PTY across acknowledged saves. Legacy
host ports retain their protective refusal and cannot establish the journal-ready
terminal contract. Both paths enforce the same acknowledgement rules, quotas,
path contract, and reserved `.proctor` root.

Every Workspace mutation carries a closed origin: `candidate` or
`execution_host`. Both origins commit through the same Attempt service and
publish the same safe realtime result. The legacy path applies only candidate-originated changes to the host;
execution-host changes are already present there and must not echo through its
unfenced `Apply`. Fenced journal projection includes every consecutive position;
its host observation confirmation protocol must suppress only exact accepted echoes. Execution-host mutations also carry the exact source
grant ID. The Store locks and verifies that this grant is still ready for the
Attempt before committing; delayed effects may acknowledge only that same grant.
Watch, Attach, and Open use the grant returned by terminal initialization, and
event retry keys include both its ID and the host cursor. Mutation provenance
is never inferred from context.

Only initialization may Ensure a host guest. Subsequent interactions use the
original connection-bound environment handle and never recreate a missing guest.
The exact-grant lease and durable state checks surround stream acquisition;
they do not remain held for a stream's lifetime. Losing the connection or a
node-local handle fails closed and requires retirement and a fresh projection,
including when a different application node must apply a lifecycle effect.

The adapter refuses unfenced `Apply` before host I/O. The normal journal adapter
uses `ApplyProjection` with the exact current fence, consecutive cursor range,
immutable content and durable request identity. Observation barriers retain
unrelated guest writes, directory operations preserve descendant identities, and
only confirmed matching guest outcomes suppress projection echoes. Full snapshots
initialize a reserved, fresh grant; they never replace a live journal projection.

Observation loss closes the terminal and releases its exact execution grant.
The host API cannot atomically reset the projection and install a replacement
watch, so in-place recovery could miss writes from an already-running guest
process. A later authorized open receives a fresh environment constructed from
durable Workspace state. Harvesting reads files through a bounded stream,
enforces the Workspace per-file limit before mutation, and uses the exact grant
and host event cursor to derive deterministic retry keys. An unacknowledgeable non-ignored
event fails the terminal rather than silently claiming durability.

An asynchronous failure fences PTY writes and terminal close, durably releases
the exact grant, and only then wakes the transport reader. The native PTY closes
immediately; transient durable-release failures retry with bounded backoff while
the reader and caller-facing close remain fenced. This ordering keeps the
connection's terminal slot occupied until a reopen is guaranteed to select a
successor grant rather than exposing the still-current placement.

The fixed ignore set excludes dependency and build trees such as `node_modules`,
`target`, `__pycache__`, and `.git`. Semantic events wholly within those trees
record a durable ignored outcome without entering the Submission. Moves across
an ignored boundary fail before any authority mutation. Over-quota or invalid
non-ignored events fail closed rather than implying durable acceptance. Semantic
directory create, move with descendants, and explicit recursive delete use the
same atomic Workspace commands as candidate operations. A kind-changing event
cannot replace an authoritative directory with a host file through an unordered
delete/create inference. Legacy watchers retain their stricter topology refusals
and never serve as a fallback for a bound semantic grant.
Non-ignored writes that cannot be acknowledged must surface an error in the
terminal. Paths outside the workspace mount, including `/tmp`, are ephemeral.

## Attempt Terminal transport

PTY bytes are not workspace mutations. They travel student to Proctor on
the existing Attempt Connection, then Proctor to the host through execenv.
The reverse path is the same. Pause, lease expiry, kick,
and manager-cannot-see-live-work apply because the Attempt Connection
already owns those gates.

Terminal open requires the latest server-persisted `expected_workspace_cursor`.
Future cursors are invalid; temporary lag returns `execution.projection_pending`
without retiring a healthy grant. Successful attachment returns a real
`environment_epoch`, acknowledged `applied_workspace_cursor`, and `projection_state`.
Candidate capability projections include these fields before creation too: the
epoch is null, cursor is zero, and state is unavailable until there is an actual
projection. Readiness never replaces the operation's current authorization checks.

Every PTY receives an opaque `terminal_id`. Input, resize, close, output, and
closed frames carry it. Stale frames, delayed output, and close callbacks must
match both the original handle and identity before touching the connection's
current terminal. PTY output and closure are not replayed, and transport loss
does not manufacture a process exit code.

PTY octets, tree bodies, grant tokens, and workspace paths never enter
ordinary logs or unsafe audit fields. Initial integrity evidence continues
to exclude terminal output and source code.

## Resources

CPU, memory, disk, and process caps are installation-defined defaults and
maxima. The Execution Profile does not request hardware. execenv reports
remaining slots; each host enforces its configured memory and other resource
caps and returns typed capacity refusal from `Ensure`. The server tries only
hosts advertising a free slot and re-places on a typed capacity refusal. The
host reports or enforces capacity; it does not pick winners. Advertising
remaining memory separately is deferred until the reusable execenv contract
exposes it.

## Implemented boundary

The server integration pins execenv v0.3.1: typed multi-host
deployment configuration and secret redaction, TLS 1.3/mTLS or loopback-only
development dialing, connection recovery, fail-closed readiness, deterministic
capability/capacity placement, durable assignment and cleanup history,
authoritative PostgreSQL/VFS workspace projection, safe retirement when an
acknowledged IDE change cannot be projected, submission revocation, and bounded desired-state
reconciliation that repairs missed open/pause/resume/release effects from
authoritative Attempt and Sitting state as well as pending revocations. Each
grant records the applied Sitting lifecycle state and revision; PostgreSQL
conditionally accepts an effect acknowledgement only while that exact state
and revision remain current. A PostgreSQL advisory lease serializes host
lifecycle effects for the exact grant across application nodes; after acquiring
it, the worker rereads authoritative Attempt and Sitting state instead of using
its triggering snapshot. Workspace initialization and incremental progress use
that same lease. Acquisition retries nonblocking advisory locks without retaining
a connection while waiting. A per-Store limit leaves at least one configured
pool connection available for ordinary queries; undersized pools fail closed.
A failed unlock discards the physical connection rather than pooling a live
session lock. The worker validates that same dedicated connection
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

Pending revocation scans advance through grant IDs in bounded pages and wrap
after reaching the end. Repeated failures on one page cannot starve later grants.

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


### Released journal adapter integration

Normal and independent server builds select the concrete ordered-control,
projection and semantic-observation adapter from execenv v0.3.1. All three
capabilities must be authenticated; interface assertions alone never prove host
support. Use matching v0.3.1 host binaries and guest images: the remote protocol
requires revision 2 and the guest helper requires revision 3. The isolated adapter
advertises journal support only with `require_journal` enabled and refuses guests
without the required native workload handshake. Enable this option only after the
installed artifacts pass Linux/KVM certification. Memory-host protocol and
PostgreSQL integration tests do not certify production isolation.

The adapter preserves original fences, node identities, versions and immutable
content. It replays from the host's retained acknowledgement when durable server
progress is ahead, confirming the exact stored outcome before ACK. Startup may
drain at most 128 pending observations before retrying projection and attaching.
Observation acquisition does not require projection catch-up; current control and
candidate authority still gate every accepted outcome. Confirmed frozen hosts wait
without destroying the observation handle. An unrelated Workspace save does not
retire a healthy PTY; each write still checks current durable execution authority.
