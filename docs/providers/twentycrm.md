---
description: >
  Describes Twenty CRM company operations, workspace binding, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-08
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace. It can list and read companies
(`read`), create them (`create`), change their name or primary domain (`update`), and delete them (`delete`).
Mutations require confirmation and only use the generated REST company route. Qatlas sends each mutation
once; after an unclear result (timeout, reset, server error, unreadable answer) it reports the outcome as
uncertain and does not repeat the request.

A `domain` value sets the primary link to `https://<domain>` with the domain as its label; an empty value
clears both. Twenty allows 100 requests per minute for each API key, and Qatlas spaces the requests of one
key accordingly.

## Deleting and restoring

- `delete` moves a company to Twenty's trash. It stays recoverable.
- `restore` brings a company back from the trash.
- `destroy` deletes a company permanently. It cannot be undone, and `restore` cannot bring the company back.
- `list` with `deleted` set to `true` lists only the companies in the trash.

`delete` and `destroy` are offered only by a connection whose `tools` list names them; `permissions` alone
does not admit them. No argument switches `delete` to permanent deletion. Each operation acts on one company
identified by its UUID, and Qatlas checks that the answer names that company.

The credential provides `api-key`. The key's workspace role remains the provider-side ceiling; the
connection's local `permissions` list can only narrow it, and an optional `tools` list, for example
`[twentycrm.companies.get]`, narrows it further to named tools without admitting an effect `permissions`
excludes. Qatlas exposes conservative core company fields,
does not accept custom-field payloads, and never lets invocation arguments replace the configured origin. The
terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and
`[twentycrm.companies.list, twentycrm.companies.get]`. A profile is a visible starting selection, not a role:
only the ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved
connection never follows a profile.
