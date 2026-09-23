---
description: >
  Describes Twenty CRM company operations, workspace binding, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-23
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace. It can list and read companies
(`read`), create them (`create`), change their name or primary domain (`update`), and delete them (`delete`).
Mutations require confirmation and only use the generated REST company route.

The credential provides `api-key`. The key's workspace role remains the provider-side ceiling; the
connection's local `permissions` list can only narrow it, and an optional `tools` list, for example
`[twentycrm.companies.get]`, narrows it further to named tools without admitting an effect `permissions`
excludes. Qatlas exposes conservative core company fields,
does not accept custom-field payloads, and never lets invocation arguments replace the configured origin. The
terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and
`[twentycrm.companies.list, twentycrm.companies.get]`. A profile is a visible starting selection, not a role:
only the ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved
connection never follows a profile.
