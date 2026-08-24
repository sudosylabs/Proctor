// Generated from CONTEXT.md. Do not edit by hand.

export type GlossaryTerm = {
  id: string;
  term: string;
  section: string;
  definition: string;
  avoid: string;
};

export const glossaryTerms = [
  {
    "id": "installation",
    "term": "Installation",
    "section": "Installation",
    "definition": "One logical deployment of Proctor representing exactly one institution, regardless of how many application processes serve it.",
    "avoid": "Tenant, university instance"
  },
  {
    "id": "institution",
    "term": "Institution",
    "section": "Installation",
    "definition": "The university or school represented by an installation.",
    "avoid": "Tenant, organization"
  },
  {
    "id": "academic-unit",
    "term": "Academic Unit",
    "section": "Academic structure",
    "definition": "A node in the institution-defined organizational hierarchy, which may contain child academic units and own programmes.",
    "avoid": "Department, faculty, school as universal core types"
  },
  {
    "id": "programme",
    "term": "Programme",
    "section": "Academic structure",
    "definition": "A course of study owned by one academic unit, such as a Bachelor of Computer Science.",
    "avoid": "Course, degree"
  },
  {
    "id": "programme-level",
    "term": "Programme Level",
    "section": "Academic structure",
    "definition": "A reusable curriculum stage within a programme, such as Foundation, Year 1, or Year 2.",
    "avoid": "Year, grade as universal core types"
  },
  {
    "id": "academic-period",
    "term": "Academic Period",
    "section": "Academic structure",
    "definition": "An enrollment period owned by the institution or one academic unit and applicable throughout that owner's academic-unit subtree, such as an academic year or semester.",
    "avoid": "School year, semester as universal core types"
  },
  {
    "id": "class",
    "term": "Class",
    "section": "Academic structure",
    "definition": "A concrete student roster for one programme level and academic period.",
    "avoid": "Group, cohort"
  },
  {
    "id": "class-member",
    "term": "Class Member",
    "section": "Academic structure",
    "definition": "A durable, time-bounded record of a student's enrollment in a class.",
    "avoid": "Student class, roster entry"
  },
  {
    "id": "academic-unit-member",
    "term": "Academic Unit Member",
    "section": "Academic structure",
    "definition": "A durable record of a user's organizational membership in an academic unit; membership alone grants no permission.",
    "avoid": "Academic role, unit permission"
  },
  {
    "id": "exam",
    "term": "Exam",
    "section": "Examinations",
    "definition": "A reusable assessment identity owned by one academic unit and delivered through one or more exam sittings.",
    "avoid": "Exam sitting, exam session"
  },
  {
    "id": "exam-draft",
    "term": "Exam Draft",
    "section": "Examinations",
    "definition": "The one mutable authoring state of an exam before its contents and policy are published as an exam revision.",
    "avoid": "Mutable exam revision, unpublished sitting"
  },
  {
    "id": "exam-revision",
    "term": "Exam Revision",
    "section": "Examinations",
    "definition": "An immutable published generation of an exam's authored content and delivery configuration.",
    "avoid": "Exam sitting, draft edit"
  },
  {
    "id": "exam-policy-set",
    "term": "Exam Policy Set",
    "section": "Examinations",
    "definition": "The typed examination and integrity rules authored in an exam draft and frozen in an exam revision.",
    "avoid": "Deployment configuration, executable policy"
  },
  {
    "id": "exam-capacity-policy",
    "term": "Exam Capacity Policy",
    "section": "Examinations",
    "definition": "The institution-owned limits on Exam Resources and Starter or Attempt Workspace growth; publication freezes the applicable policy in an exam revision so later institution changes cannot alter admitted attempts.",
    "avoid": "Execution-host capacity, deployment configuration, retention policy"
  },
  {
    "id": "exam-sitting",
    "term": "Exam Sitting",
    "section": "Examinations",
    "definition": "A scheduled delivery of one exam revision to one class.",
    "avoid": "Exam version, exam session"
  },
  {
    "id": "exam-attempt",
    "term": "Exam Attempt",
    "section": "Examinations",
    "definition": "One student's private, durable body of work while participating in an exam; its acknowledged work survives interruption and submission.",
    "avoid": "Exam session, exam member"
  },
  {
    "id": "attempt-participation",
    "term": "Attempt Participation",
    "section": "Examinations",
    "definition": "One fenced, server-recognized period of continuous candidate participation in an exam attempt.",
    "avoid": "Exam attempt, connection, authentication session"
  },
  {
    "id": "participation-generation",
    "term": "Participation Generation",
    "section": "Examinations",
    "definition": "The sequential identity of one attempt participation; once ended or expired, it can never authorize work again.",
    "avoid": "Connection number, retry count"
  },
  {
    "id": "participation-lease",
    "term": "Participation Lease",
    "section": "Examinations",
    "definition": "The renewable server-authoritative deadline proving that one participation generation remains current.",
    "avoid": "WebSocket ping, authentication session, client timer"
  },
  {
    "id": "attempt-connection",
    "term": "Attempt Connection",
    "section": "Examinations",
    "definition": "One authenticated transport connection within an attempt participation.",
    "avoid": "Exam attempt, participation"
  },
  {
    "id": "attempt-suspension",
    "term": "Attempt Suspension",
    "section": "Examinations",
    "definition": "One reversible episode in which manual or policy enforcement blocks an active exam attempt until an authorized re-allow decision.",
    "avoid": "Submission, guilt finding, disconnection"
  },
  {
    "id": "exam-instructions",
    "term": "Exam Instructions",
    "section": "Examinations",
    "definition": "The authored problem statement and directions presented to students for an exam.",
    "avoid": "Subject"
  },
  {
    "id": "exam-resource",
    "term": "Exam Resource",
    "section": "Examinations",
    "definition": "A read-only file deliberately projected inside the protected exam client to support understanding or completion of an exam.",
    "avoid": "Subject file, attachment when its exam meaning matters"
  },
  {
    "id": "starter-workspace",
    "term": "Starter Workspace",
    "section": "Examinations",
    "definition": "The optional published hierarchy of initial code and directories copied into a new attempt workspace.",
    "avoid": "Exam resource, shared workspace"
  },
  {
    "id": "attempt-workspace",
    "term": "Attempt Workspace",
    "section": "Examinations",
    "definition": "The isolated, remotely authoritative hierarchy of mutable working files belonging to one exam attempt.",
    "avoid": "Shared workspace, local folder"
  },
  {
    "id": "execution-environment",
    "term": "Execution Environment",
    "section": "Examinations",
    "definition": "The isolated, non-authoritative projection of one attempt workspace in which a candidate may use an attempt terminal.",
    "avoid": "Code runner, coderunner, microVM, sandbox, local folder"
  },
  {
    "id": "attempt-terminal",
    "term": "Attempt Terminal",
    "section": "Examinations",
    "definition": "One interactive PTY attached to an execution environment for one exam attempt.",
    "avoid": "SSH session, local terminal, code runner"
  },
  {
    "id": "execution-profile",
    "term": "Execution Profile",
    "section": "Examinations",
    "definition": "The authored, revision-frozen choice of whether an exam offers an attempt terminal, which catalog image it uses, and which network mode applies.",
    "avoid": "Devcontainer, Dockerfile, deployment configuration, executable policy"
  },
  {
    "id": "execution-image",
    "term": "Execution Image",
    "section": "Examinations",
    "definition": "A named, installation-provided guest runtime a creator may select in an execution profile.",
    "avoid": "Dockerfile, devcontainer, rootfs"
  },
  {
    "id": "workspace-entry",
    "term": "Workspace Entry",
    "section": "Examinations",
    "definition": "A stable logical file or directory in one attempt workspace whose identity survives path changes.",
    "avoid": "File entry, storage object, path"
  },
  {
    "id": "workspace-content-version",
    "term": "Workspace Content Version",
    "section": "Examinations",
    "definition": "An opaque comparison token for one acknowledged current content state of a workspace file; it is not a retained file revision.",
    "avoid": "File revision, timestamp"
  },
  {
    "id": "integrity-evidence",
    "term": "Integrity Evidence",
    "section": "Examinations",
    "definition": "Bounded, purpose-specific material retained to support an integrity flag while preserving its provenance, uncertainty, and known gaps.",
    "avoid": "Audit log, cheating proof"
  },
  {
    "id": "integrity-flag",
    "term": "Integrity Flag",
    "section": "Examinations",
    "definition": "A recorded indication of suspected examination-rule violation; it is evidence for review, not a finding of guilt.",
    "avoid": "Cheating verdict, automatic violation"
  },
  {
    "id": "connection-loss",
    "term": "Connection Loss",
    "section": "Examinations",
    "definition": "The server-confirmed expiry of the current participation lease for an active exam attempt.",
    "avoid": "One failed request, WebSocket ping failure, guilt finding"
  },
  {
    "id": "focus-loss",
    "term": "Focus Loss",
    "section": "Examinations",
    "definition": "A bounded client observation that the protected exam experience lacked focus for a qualifying duration.",
    "avoid": "Cheating verdict, connection loss"
  },
  {
    "id": "submission",
    "term": "Submission",
    "section": "Examinations",
    "definition": "A single immutable manifest sealing the exact acknowledged workspace state at the end of one exam attempt.",
    "avoid": "Attempt, copied workspace, grade"
  },
  {
    "id": "submission-review",
    "term": "Submission Review",
    "section": "Examinations",
    "definition": "The integrity decisions and manager remarks associated with one submission; it contains no structured academic grade or outcome.",
    "avoid": "Grade, rubric, submission"
  },
  {
    "id": "exam-manager",
    "term": "Exam Manager",
    "section": "Examinations",
    "definition": "The exam creator or a teacher explicitly granted equal authority to manage one exam; system-administrator override is not membership in this set.",
    "avoid": "Proctor, grader"
  },
  {
    "id": "file-content",
    "term": "File Content",
    "section": "File content",
    "definition": "The durable bytes and immutable representations belonging to file revisions, including any bounded normalized or extracted forms derived from those bytes. Whether content may be stored, searched, retained, or exposed remains meaning owned by the file entry's application purpose.",
    "avoid": "Live workspace state, authorization policy, search permission"
  },
  {
    "id": "file-entry",
    "term": "File Entry",
    "section": "File content",
    "definition": "A stable, application-visible file identity with one owner and purpose across changes to its content.",
    "avoid": "FileInfo, blob"
  },
  {
    "id": "file-revision",
    "term": "File Revision",
    "section": "File content",
    "definition": "One immutable generation of a file entry's content and bounded descriptive metadata.",
    "avoid": "File version when referring to an exam version"
  },
  {
    "id": "file-rendition",
    "term": "File Rendition",
    "section": "File content",
    "definition": "One stored representation of a file revision, such as a normalized image size; several renditions do not represent separate content changes.",
    "avoid": "File revision, copy"
  },
  {
    "id": "workspace-path",
    "term": "Workspace Path",
    "section": "File content",
    "definition": "The mutable, normalized, case-sensitive POSIX-relative location of a workspace entry within one attempt workspace.",
    "avoid": "Workspace identity, VFS path, object key"
  },
  {
    "id": "default-profile-picture",
    "term": "Default Profile Picture",
    "section": "File content",
    "definition": "The system-generated, permanently retained fallback image belonging to one user when no custom profile picture is active.",
    "avoid": "Placeholder URL, shared avatar"
  },
  {
    "id": "job",
    "term": "Job",
    "section": "Durable work",
    "definition": "A finite body of background work whose progress and outcome are retained and which may continue through interruption or retry.",
    "avoid": "Runtime loop, goroutine, recurring service"
  },
  {
    "id": "job-attempt",
    "term": "Job Attempt",
    "section": "Durable work",
    "definition": "One recorded try to complete a job; several job attempts may belong to the same job. It is distinct from an exam attempt.",
    "avoid": "Exam Attempt, Job"
  },
  {
    "id": "user",
    "term": "User",
    "section": "Identity and access",
    "definition": "A login-capable Proctor account containing profile and account state, but not credentials, affiliations, roles, or permissions.",
    "avoid": "Person, account when referring specifically to the Proctor identity"
  },
  {
    "id": "user-settings-document",
    "term": "User Settings Document",
    "section": "Identity and access",
    "definition": "One portable, user-owned source document containing client presentation preferences; it grants no capability and belongs to neither deployment configuration nor an exam workspace.",
    "avoid": "IDE preferences file, workspace settings, deployment configuration"
  },
  {
    "id": "affiliation",
    "term": "Affiliation",
    "section": "Identity and access",
    "definition": "A time-bounded, non-exclusive relationship between a user and the institution, such as student, teacher, staff member, or external collaborator.",
    "avoid": "User type, role"
  },
  {
    "id": "external-identity",
    "term": "External Identity",
    "section": "Identity and access",
    "definition": "A link between a user and an opaque subject asserted by a configured external identity provider.",
    "avoid": "SSO user, provider account"
  },
  {
    "id": "access-policy",
    "term": "Access Policy",
    "section": "Identity and access",
    "definition": "The revisioned institution application policy selecting which configured authentication and account-admission capabilities are currently available.",
    "avoid": "Authentication mode, deployment configuration, client settings"
  },
  {
    "id": "invitation",
    "term": "Invitation",
    "section": "Identity and access",
    "definition": "A durable pre-user authorization for one person to claim an account and one explicit institutional relationship or scoped role package.",
    "avoid": "User token, pending user, email message"
  },
  {
    "id": "desktop-authorization",
    "term": "Desktop Authorization",
    "section": "Identity and access",
    "definition": "One short-lived browser-to-desktop handoff that may create a Proctor desktop session after exact callback, state, and proof verification.",
    "avoid": "Device token, external-provider token, desktop login page"
  },
  {
    "id": "principal",
    "term": "Principal",
    "section": "Identity and access",
    "definition": "The authenticated security identity acting in a request or operation, including its credential and authentication context.",
    "avoid": "User when authentication context matters"
  },
  {
    "id": "role",
    "term": "Role",
    "section": "Identity and access",
    "definition": "A named collection of permitted actions.",
    "avoid": "Affiliation, user type"
  },
  {
    "id": "role-binding",
    "term": "Role Binding",
    "section": "Identity and access",
    "definition": "A time-bounded assignment of a role to a user at a defined authorization scope.",
    "avoid": "Membership, permission"
  },
  {
    "id": "action",
    "term": "Action",
    "section": "Identity and access",
    "definition": "A stable domain operation that authorization may permit, such as viewing class members or managing an academic unit.",
    "avoid": "Endpoint, HTTP permission"
  },
  {
    "id": "resource",
    "term": "Resource",
    "section": "Identity and access",
    "definition": "The domain object or scope against which an action is authorized.",
    "avoid": "Route, record"
  },
  {
    "id": "session",
    "term": "Session",
    "section": "Identity and access",
    "definition": "A revocable server-side authentication context for an interactive client; it contains no bearer credential or role snapshot.",
    "avoid": "Login token, personal access token"
  },
  {
    "id": "personal-access-token",
    "term": "Personal Access Token",
    "section": "Identity and access",
    "definition": "A finite, revocable credential for API access on behalf of a user, constrained by explicit action scopes and optionally by academic scope.",
    "avoid": "Session, API session"
  },
  {
    "id": "user-token",
    "term": "User Token",
    "section": "Identity and access",
    "definition": "A purpose-specific, expiring, single-use credential for an account operation such as email verification or password reset.",
    "avoid": "Session token, personal access token"
  }
] satisfies readonly GlossaryTerm[];

export const glossaryById = Object.fromEntries(
  glossaryTerms.map((term) => [term.id, term]),
) as Readonly<Record<string, GlossaryTerm>>;
