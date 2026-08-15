# Security and privacy

Proctor stores sensitive educational and identity data. Security properties
are explicit contracts backed by tests, not assumptions inherited from an
upstream reference.

## Boundary safeguards

- Require TLS outside explicit local development.
- Trust forwarding headers only from configured proxies.
- Bound request bodies, headers, URLs, provider responses, uploads, queues, and
  serialized diagnostic fields.
- Use established password hashing and cryptographic libraries and
  constant-time credential comparisons where applicable.
- Rate-limit login, bootstrap, token, invitation, and recovery operations and
  prevent account enumeration with uniform public responses.
- Persist credentials as hashes when they need only comparison; encrypt secrets
  only when the server must recover them.
- Authorize file access from semantic metadata rather than guessed paths; bound
  uploaded type/size and retain a malware/content-scanning boundary.
- Use least-privilege database, storage, mail, and provider credentials.
- Define retention and legal-preservation behavior before destructive student
  data workflows.
- Test horizontal privilege escalation and cross-academic-unit isolation.

## Authentication attempt accounting

Login, account recovery, external-authentication initiation, and installation
bootstrap share one private attempt-accounting implementation while retaining
their distinct security policies and public errors. Fixed purpose and dimension
names select domain-separated key spaces; identity, source, operation, and
provider values contribute only to a cryptographic digest. User-controlled
values never select arbitrary key namespaces.

Attempt counters use the disposable cache with atomic per-key increments and a
sliding inactivity window. Multi-dimension attempts increment each applicable
counter sequentially before evaluating the combined limit. Cache failure fails
closed, including a failure after an earlier dimension was incremented. A
successful password login clears only its combined identity/source counter;
the source-wide counter continues to protect against attempts spread across
identities. Changing these dimensions, ordering, window semantics, or reset
behavior is a security-policy change rather than an implementation refactor.

## Examination containment and integrity

The examination security boundary is defined in
[Examinations](./examinations.md). Exam Resources, Starter Workspaces, Attempt
Workspaces, and Submissions are available to candidates only through the
protected application experience. Candidate routes do not provide public or
signed object URLs, download or export operations, printing, external-open,
drag-out, or local-folder projection. Rendering still transfers bounded bytes
to the authorized client; the enforceable contract is export containment, not
the false claim that content never reaches the device.

Exam instructions and resource descriptions are untrusted authored Markdown.
Presentation sanitizes active content, unsafe URLs, and automatic remote loads;
the server does not moderate academic meaning. Workspace paths are logical
PostgreSQL metadata and never VFS keys, authorization inputs, or evidence that
a directory exists in object storage.

Client security observations are authenticated, versioned, bounded,
generation-fenced claims rather than authoritative verdicts. The server owns
receipt time, connection-loss observation, policy evaluation, enforcement,
Integrity Flags, and retained evidence. Raw instructions, resource bytes,
workspace paths or contents, source code, private manager reasons, and review
remarks do not enter ordinary logs, realtime events, or generic audit fields.
Authorized managers may inspect sealed Submissions and bounded evidence only
through their application permissions. The integrity review records decisions
and remarks; it is not a grading or academic-outcome subsystem.

Focus Loss arrives only through the authenticated Attempt WebSocket binding.
The strict claim supplies the required supported schema version, current
generation, monotonic signal sequence, bounded integer duration, optional
closed source classification, and the same continuity credential; the
application hashes that credential before crossing the Store boundary. Audit
fields retain only safe identities, generation, and sequence. Candidate
acknowledgements and events omit duration, source, raw policy, qualification,
thresholds, Flag state, credentials, and Session identity. Manager events
expose only the bounded Flag projection: safe identities, configured outcome,
retained/overflow counts, and server time.

Participation renewal is an application-level authenticated protocol separate
from WebSocket liveness. PostgreSQL lease expiry is authoritative, permanently
fences the old generation, and always creates neutral Connection Loss evidence,
one Flag, and an automatic Attempt suspension. A candidate cannot resume until
an authorized manager records a private reason and re-allows the Attempt, after
which fresh admission creates a new generation. Candidate messages describe
lost secure continuity without exposing credentials, internal generation
vocabulary, or asserting guilt.

## Logging and observability

The Proctor-owned `mlog` subsystem supports independently filtered text or JSON
targets, safe contextual fields, bounded serialization, atomic dynamic
reconfiguration, test capture, flushing, and shutdown. `platform.Service` owns
its lifecycle.

Unexpected failures are logged once at the outer operational boundary.
Request-driven application services return failures rather than logging them;
long-lived workers receive a narrow logging port only for otherwise
unobservable operational events. Context may include component, request ID,
node ID, and safe entity IDs—never complete users, sessions, configuration,
credentials, tokens, exam answers, provider payloads, or raw claims.

Metrics and tracing instrument HTTP, WebSocket, store, and outbound-adapter
boundaries through explicit wrappers. Application use cases may expose named
outcomes or events but do not call a global telemetry facade.

Durable security audit behavior is specified in
[Authorization and audit](./authorization.md#audit).
