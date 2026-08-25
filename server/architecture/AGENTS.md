# Architecture-test agent guide

This directory enforces repository architecture and documentation invariants.
Invoke
[`$server-boundaries`](../../.agents/skills/server-boundaries/SKILL.md)
before changing import policy and invoke
[`$documentation-design`](../../.agents/skills/documentation-design/SKILL.md)
before changing documentation or skill validation.

- Tests remain hermetic and network-free.
- Production package boundaries are declared as ordered rules in
  `import_policy_test.go`. Every production package must match a rule and every
  forbidden import fails the gate; do not add waiver or debt mechanisms.
- Documentation validation inspects repository candidates when Git metadata is
  available and skips only the Git-specific check for an independently tested
  server module without a repository checkout.
- Report every violating file/link in one run so maintainers can repair the
  complete documentation graph.

Run `make -C server architecture` after changes here.
