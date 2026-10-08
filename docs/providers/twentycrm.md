---
description: >
  Describes Twenty CRM company and object catalog operations, object targets, connection permissions, and
  safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-08
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace and, optionally, to a set of its
objects (see Object targets). It can list and read companies (`read`), create them (`create`), change their
name or primary domain (`update`), and delete them (`delete`).
Mutations require confirmation and only use the generated REST company route. Qatlas sends each mutation
once; after an unclear result (timeout, reset, server error, unreadable answer) it reports the outcome as
uncertain and does not repeat the request.

A `domain` value sets the primary link to `https://<domain>` with the domain as its label; an empty value
clears both. Twenty allows 100 requests per minute for each API key, and Qatlas spaces the requests of one
key accordingly.

## Object targets

A target has the form `object/NAME` with the singular camelCase API name of an object, for example
`object/person`. A connection may list several; a value without the `object/` form fails validation.

- With targets, the connection reaches exactly the bound objects.
- Without targets, the connection reaches every non-system object its API key reaches. Set `object/` targets
  on every connection that should not see all of them: tools for further objects make, for example, person
  data reachable on a connection without targets.
- System objects (messages, calendar events, attachments, workflows, workspace members, and the like) are
  never reachable and cannot be a target. Qatlas keeps them in a fixed list, so an object a later Twenty
  version marks as a system object stays reachable on a connection without targets until the list is
  updated.
- `companies.*` need `company` to be reachable: with targets, `object/company` must be bound.
- Workspace-wide tools work only on a connection without object targets.

`objects.list` and `objects.get` (`read`) show the reachable objects and, per object, each field with its
JSON type, subfields of composite fields, whether a value is required or can be written, and the reachable
object a relation points to. Relations to objects the connection does not reach are left out. The catalog is
read from the workspace's generated API document (`/open-api/core`), which needs no right beyond the API
key's ordinary access, not from the metadata API, which needs the settings right. Only names, types, and
flags leave Qatlas; descriptions and options of the document never do.

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
terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and the two company
and two object read tools. A profile is a visible starting selection, not a role:
only the ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved
connection never follows a profile.
