# Architecture-test agent guide

This directory enforces repository architecture and documentation invariants.
Read [`docs/architecture/dependencies.md`](../../docs/architecture/dependencies.md)
before changing import policy and
[`docs/contributing/documentation.md`](../../docs/contributing/documentation.md)
before changing the Markdown validator.

- Tests remain hermetic and network-free.
- The production dependency-debt ledger may shrink but never grow. Its current
  accepted state is empty.
- Documentation validation inspects repository candidates when Git metadata is
  available and skips only the Git-specific check for an independently tested
  server module without a repository checkout.
- Report every violating file/link in one run so maintainers can repair the
  complete documentation graph.

Run `make -C server architecture` after changes here.
