---
description: >
  Describes Twenty CRM company, object catalog, record read (list, get, structured search, count by
  field), record write (create, update), record trash (delete, restore, destroy), and note and task link
  operations, object targets, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-08
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace and, optionally, to a set of its
objects (see Object targets). It can list and read companies (`read`), create them (`create`), change their
name or primary domain (`update`), and delete them (`delete`). It can also create records of any reachable
object and change their fields (see Writing records), delete, restore, and destroy them (see Deleting and
restoring), and link notes and tasks to records (see Linking notes and tasks).
Mutations require confirmation and only use the generated REST routes of the company or of a reachable
object. Qatlas sends each mutation once; after an unclear result (timeout, reset, server error, unreadable
answer) it reports the outcome as uncertain and does not repeat the request.

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
  never reachable and cannot be a target. The two link objects of notes and tasks are system objects too;
  only the link tools touch them, and no records tool does. Qatlas keeps them in a fixed list, so an object a
  later Twenty version marks as a system object stays reachable on a connection without targets until the
  list is updated.
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
argument. A list returns one page without filters (see Filtering and counting records for conditions), 1 to 100
records, with an opaque `next_cursor`.
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
  Qatlas adds no right. Soft-deleted records are read only by `records.list` with `deleted`.

## Filtering and counting records

`records.search` and `records.groupby` (`read`) take no filter expression. The agent gives structured
conditions and Qatlas builds Twenty's filter from checked parts only: Twenty's grammar has no escaping, so no
value may carry a quote, bracket, colon, comma, backslash, parenthesis, or percent sign.

- `records.search` reads a page like `records.list` (page size, sort, `fields`, cursor) for 1 to 5 AND-linked
  conditions `{field, operator, value}`. The cursor is also bound to the normalized conditions, whose order
  and repeats do not matter.
- `records.groupby` counts the records per value of 1 or 2 different fields, optionally limited by the same
  conditions, and returns groups with `values` (field name to value) and `count`.
- A condition field is a top-level field of the object's response shape or `field.subfield` of a composite
  field, only when the schema names the subfield. Relation fields, `deletedAt`, rich text, arrays and
  multi-selections, and subfields that hold identifiers are not filterable. Soft-deleted records are never
  searched.
- A relation identifier (`<relation>Id`) is accepted only when the relation field names a target object that
  the connection reaches; otherwise the condition could probe objects outside the targets.
- The operator list follows the field kind, which Qatlas derives from the schema (enum values make a
  selection, a format makes an identifier or date, otherwise the JSON type decides):

  | Kind | Operators |
  | --- | --- |
  | text | `eq`, `neq`, `in`, `ilike` (contains), `startsWith`, `endsWith`, `is` |
  | selection | `eq`, `neq`, `in`, `is`; the value must be an enum value of the schema |
  | number, date, date-time | `eq`, `neq`, `in`, `gt`, `gte`, `lt`, `lte`, `is` |
  | boolean | `eq`, `is` |
  | identifier, relation identifier | `eq`, `neq`, `in`, `is` |

- `is` takes `NULL` or `NOT_NULL`; `in` takes a list of at most 20 values of one type. Dates are
  `YYYY-MM-DD`, date-times `YYYY-MM-DDTHH:MM:SSZ`, numbers plain decimals. Text is at most 64 characters of
  letters, digits, spaces, and `. _ + @ & -`.
- A refused field, operator, or value is refused after the schema was read and before any record is read,
  and the refusal names neither field nor value. The enum values of the schema serve only this check and
  never appear in a result, including `objects.get`.
- `records.groupby` groups by selections, booleans, dates (by day, UTC), and relation identifiers, not by text,
  numbers, composite subfields, or the record `id`. Twenty cuts a longer group list silently, so an answer
  with 200 or more groups is refused as possibly incomplete; narrow it with conditions. Group values are
  untrusted workspace data of the same data class as records.

## Writing records

`records.create` and `records.update` (`create`, `update`) write one record of any reachable object. `create`
posts the given `fields`; `update` patches only the fields named for the record `id` and needs at least one.
Neither upserts or writes in batches. Qatlas takes the object, its route, and the shape of every
field from the workspace catalog and builds the request body from the checked values only.

- Every field is checked against the schema before the request: a `create` against the create shape of the
  object, with its required fields, an `update` against the update shape, without required fields. The
  schema read is the only other request, and a refusal after it sends no write. A refusal names neither
  field nor value.
- Supported are text, number, boolean, date (`YYYY-MM-DD`) and date-time (RFC 3339), selection and
  multi-selection (values of the schema), emails, phones, links, currency (`amountMicros`, `currencyCode`),
  full name, and address. A composite value may contain only the parts the schema names, each of its type.
  Null is refused; send an empty value to clear a text.
- Rich text takes only `{"markdown": ...}`; Twenty derives the block document from it. A read reports it the
  same way.
- A relation is set by its identifier field (`<relation>Id`, a UUID) and only when the relation's target is
  reachable through the connection; otherwise the field is refused without naming the target. Identifier
  fields that are not a relation to a reachable object are refused as well.
- Refused fields: system fields (`id`, `createdAt`, `updatedAt`, `deletedAt`, `createdBy`, `updatedBy`,
  `position`), fields the schema does not offer for the operation, files, actor fields, free JSON, and lists
  of plain text.
- At most 100 fields per call, 64 KiB per string, 1 MiB per request. The answer is the written record,
  read at depth 0 and reduced like a read; it must name the requested record.
- The values written and returned are record data of their own data class and appear only in a result.
- Qatlas sends the write once. After an unclear result (timeout, reset, server error, unreadable answer) it
  reports the outcome as uncertain and does not repeat. For `create` the report warns that repeating adds a
  duplicate record; search the object first.
- A 403 means the role of the API key lacks the right to write the object or one of the fields; Twenty's own
  text is never shown.

The setup profile `write` ticks the read tools, these two record tools, and the link create tool. It adds no
company change and no deletion or restore.

## Linking notes and tasks

`activitytargets.list`, `activitytargets.create`, and `activitytargets.delete` (`read`, `create`, `delete`)
read, set, and remove the links between a note or a task and a record of any reachable object, standard or
custom. The argument `activity` (`note` or `task`) selects the link object. The agent names the object and the
record identifier; the field that carries the link comes from the workspace schema of the link object, never
from an argument. An object that the schema gives no single link relation cannot be linked.

- The note or task and the object must both be reachable through the connection, so with targets both
  `object/note` (or `object/task`) and the object are bound. A refusal comes before any secret is resolved
  and names neither object nor identifier.
- `list` takes either `activity_id` (the links of a note or task) or `object` with `record_id` (the notes or
  tasks linked to a record), one page with cursor like the record reads. It names only links whose two sides
  are reachable and counts the others in `omitted`.
- `create` sends one request with the activity and the one target identifier. Repeating it adds a duplicate
  link, so an unclear result is reported as uncertain and not repeated. The answer must name the requested link.
- `delete` removes only the link, never the note, task, or record. It reads the link first and removes it only
  when both sides are reachable, then sends one request. An unclear result is reported as uncertain and not
  repeated. A connection offers it only when its `tools` list names it.
- Identifiers are UUIDs. Links are workspace data of the record data class.

## Deleting and restoring

Companies and records of any reachable object share one trash contract:

- `delete` moves one company or record to Twenty's trash. It stays recoverable.
- `restore` brings one company or record back from the trash.
- `destroy` deletes one company or record permanently. It cannot be undone, and `restore` cannot bring it
  back. Twenty's answer does not tell soft from permanent deletion, so the result only confirms that Twenty
  accepted the request.
- `list` with `deleted` set to `true` lists only what is in the trash; this holds for `companies.list` and
  `records.list`, not for `records.search`.

The record tools are `records.delete`, `records.restore`, and `records.destroy`. Each acts on one record of one
reachable object, identified by its UUID, and sends one fixed route; no argument switches `delete` to permanent
deletion or adds a filter, so there is no deletion or restore by filter or by list of identifiers. A refused
object is rejected before the secret is read. Qatlas checks that the answer names the requested record. A 403 on
`records.delete` and `records.destroy` points to the right of the role of the key (Delete Records, Destroy
Records); Twenty's own text is never shown. Qatlas sends each of them once and reports an unclear result as
uncertain.

`delete` and `destroy`, for companies and records, are offered only by a connection whose `tools` list names
them; `permissions` alone does not admit them. No profile ticks them or `records.restore`.

The credential provides `api-key`. The key's workspace role remains the provider-side ceiling; the
connection's local `permissions` list can only narrow it, and an optional `tools` list, for example
`[twentycrm.companies.get]`, narrows it further to named tools without admitting an effect `permissions`
excludes. The `companies.*` tools expose conservative core company fields and accept no custom-field
payloads; custom fields are written through `records.create` and `records.update`. Invocation arguments never
replace the configured origin. The terminal editor starts a new connection on the setup profile `read`, which
ticks `[read]` and the two company, two object, four record read tools, and the link list tool; the profile
`write` adds the two record write tools and the link create tool. A profile is a visible starting selection,
not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed before saving,
and a saved connection never follows a profile.
