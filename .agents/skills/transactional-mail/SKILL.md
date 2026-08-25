---
name: transactional-mail
description: Add or change server-owned transactional mail definitions, templates, localization, recipients, fan-out, frozen payloads, encryption, durable delivery, retry, suppression, key rotation, metrics, retention, or operator controls.
---

# Change transactional mail

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) for domain mail events or public
   copy. Completion: message families use canonical domain identities and safe
   bounded presentation data.
2. Read the relevant section of the
   [transactional-mail reference](references/mail.md) and the exact
   [template workflow](../../../server/templates/README.md) when templates or
   locales change. Completion: definition ownership, recipients, atomicity,
   payload protection, delivery, retry, and retention are explicit.
3. Keep MIME/SMTP mechanics in `packages/mail`; keep product meaning,
   preparation, localization, suppression, frozen payloads, and durable policy
   in `app/mail`. Completion: no mail is sent inside a business transaction.
4. Commit the business mutation, audit, Mail Occurrence, Deliveries, and Job
   through the named aggregate contract before delivery. Completion: replay
   preserves stable recipient meaning and Message-ID without duplicate effects.
5. Update definition completeness, template snapshots, localization, Store,
   encryption, fan-out, retry, disabled-mode, metrics, retention, and operator
   tests, then run the transactional-mail phase target and architecture gate.
