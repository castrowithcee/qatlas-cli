---
description: >
  Describes Twenty CRM company operations, workspace binding, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-12
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace. It can list and read companies
(`read`), create them (`create`), change their name or primary domain (`update`), and delete them (`delete`).
Mutations require confirmation and only use the generated REST company route.

The credential provides `api-key`. The key's workspace role remains the provider-side ceiling; the
connection's local `permissions` list can only narrow it. Qatlas exposes conservative core company fields,
does not accept custom-field payloads, and never lets invocation arguments replace the configured origin.
