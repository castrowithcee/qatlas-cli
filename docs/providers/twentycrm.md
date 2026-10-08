---
description: >
  Describes Twenty CRM company, object catalog, and record read operations, object targets, connection
  permissions, and safety boundaries.
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

## Reading records

`records.list` and `records.get` (`read`) read the records of any reachable object, standard or custom, by its
singular API name. Qatlas takes the REST path of an object only from the workspace catalog, never from an
argument. A list returns one page without filters, 1 to 100 records, with an opaque `next_cursor`.
`order_by` sorts by one field, or by `field.subfield` of a composite field, in direction `asc` or `desc`.
Relation, array, and rich-text fields cannot be sorted by.

- `fields` names at most 50 top-level fields and is sent to Twenty as its `fields` parameter. Qatlas checks
  `fields` and `order_by` against the response shape of the object in the workspace document, so an unknown
  field is refused before any record is read, and the refusal does not name it. The timestamps of a record
  are always read along.
- Each record carries `id`, `created_at`, `updated_at`, and `fields` (field name to value). Qatlas always
  reads at depth 0: a relation appears only as its identifier field (`companyId`), never as an embedded
  record, and a relation to an object the connection cannot reach stays an identifier. Rich text appears
  only as `markdown`, never as its block document.
- A cursor is bound to the connection, object, sort, and field selection that produced it; any other use is
  refused before a request is sent.
- A value above the bounds (64 KiB per string, 4 levels of nesting, 100 entries per array) or an answer
  above the response limit is an invalid response; Qatlas does not cut it.
- Record values are untrusted workspace content and often personal data (names, emails, phone numbers,
  notes). They appear only in a result, never in errors, logs, or audit entries, and carry their own data
  class. Set `object/` targets to keep a connection away from objects whose records it does not need.
- The role of the API key is the ceiling: Twenty returns only the objects and fields the role grants, and
  Qatlas adds no right. Soft-deleted records are not read.

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
terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and the two company,
two object, and two record read tools. A profile is a visible starting selection, not a role:
only the ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved
connection never follows a profile.
