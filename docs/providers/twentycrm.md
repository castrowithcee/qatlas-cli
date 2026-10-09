---
description: >
  Describes Twenty CRM company, object catalog, record read (list, get, structured search, search across
  objects, count by field), record write (create, update, batches), duplicate search and merge, record
  trash (delete, restore, destroy), note and task link operations, workflow, member, and role read, object
  targets, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-09
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace and, optionally, to a set of its
objects (see Object targets). It can list and read companies (`read`), create them (`create`), change their
name or primary domain (`update`), and delete them (`delete`). It can also create records of any reachable
object and change their fields, also up to 60 at a time (see Writing records and Batches), delete, restore,
and destroy them (see Deleting and restoring), find duplicates and merge records (see Merging records),
and link notes and tasks to records (see Linking notes and tasks).
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
  never reachable through the object and record tools and cannot be a target; workflows have their own
  read tools (see Reading workflows). The two link objects of notes and tasks are system objects too;
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

- `is` takes `NULL` or `NOT_NULL`; `in` takes a list of at most 20 values of one type. Values are plain
  text, numbers, `YYYY-MM-DD` dates, or `YYYY-MM-DDTHH:MM:SSZ` date-times; the help text lists the characters.
- A refused field, operator, or value is refused after the schema was read and before any record is read,
  and the refusal names neither field nor value. The enum values of the schema serve only this check and
  never appear in a result, including `objects.get`.
- `records.groupby` groups by selections, booleans, dates (by day, UTC), and relation identifiers, not by text,
  numbers, composite subfields, or the record `id`. Twenty cuts a longer group list silently, so an answer
  with 200 or more groups is refused as possibly incomplete; narrow it with conditions. Group values are
  untrusted workspace data of the same data class as records.

## Searching across objects

`records.searchall` (`read`) finds records by full-text search over all reachable objects in one call and
returns per hit `object`, `id`, and `label` (display name, capped), plus `omitted`. Read a hit with
`records.get`. It is the only tool that uses Twenty's GraphQL endpoint. Qatlas sends one fixed query and
passes the checked arguments only as variables; no argument becomes part of a query, and there is no free
GraphQL. Twenty reports a failed query as an answer with errors; Qatlas maps it to a class and shows no
text of it.

- `objects` limits the search to a subset of the reachable objects; an object outside the connection is
  ignored, and an empty remaining set is refused before the search is sent.
- The searched objects are always named explicitly: the bound objects with targets, otherwise the
  non-system objects of the workspace catalog, each cut with `objects`. Hits of any other object are
  dropped and only counted in `omitted`.
- No image URLs, rank values, or search filters are read. The cursor is bound to the connection, its
  targets, `text`, and `objects`.

## Reading workflows

`workflows.list`, `workflows.get`, and `workflowruns.list` (`read`) let an agent follow automations and find
failed runs. They work only on a connection without object targets and are refused before the key is read
otherwise. `workflow`, `workflowVersion`, and `workflowRun` are system objects, so the record tools never reach
them; these tools use fixed routes and a fixed field selection.

- `workflows.get` adds the 50 newest versions to a workflow; `workflowruns.list` returns the runs of one
  workflow, newest first, optionally of one status.
- Step and trigger settings, run outputs, context, state, and error texts are never read out. Status,
  trigger, and step types come from fixed lists; any other value is shown as `unknown`.
- Starting a workflow run is not possible with an API key: Twenty refuses it for keys, and Qatlas does not
  work around that.

## Reading members and roles

`workspacemembers.list`, `workspacemembers.get`, and `roles.list` let an agent see who can be assigned work
and which roles exist. They work only on a connection without object targets. `workspaceMember` is a
system object, so the record tools never reach it; these tools use a fixed route and a fixed field selection.

- Members are personal data: name, email, time zone, and locale, never avatars or user identifiers. They
  appear only in a result, never in errors, and carry their own data class.
- `roles.list` shows per role its label, assignability, six global rights, and the number of assigned
  members and API keys, from one fixed query on the metadata API; no member or API key name, and no object
  or field right, is read. The data class is the role class.
- `roles.list` needs the Twenty right "Roles", which also allows changing roles and permissions. Use a
  connection with an API key of its own for it. No profile ticks it.

## Writing records

`records.create` and `records.update` (`create`, `update`) write one record of any reachable object. `create`
posts the given `fields`; `update` patches only the fields named for the record `id` and needs at least one.
Neither upserts or writes several records; see Batches. Qatlas takes the object, its route, and the shape of
every field from the workspace catalog and builds the request body from the checked values only.

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

## Batches

`records.batchcreate`, `records.batchupdate`, and `records.batchdelete` act on one reachable object with 1 to
60 records in one confirmed request that Qatlas sends once.

- `batchcreate` takes a list of records; each is checked like a `create`, and one refused record refuses the
  batch before anything is written. With `upsert` Twenty matches the entries against existing records by the
  unique fields of the object and changes the match instead of creating a record. Because `upsert` can change
  records, the tool carries the effect `update` and is not idempotent: a connection needs the `update`
  permission for it even without `upsert`.
- `batchupdate` sets one set of fields, checked like an `update`, on a list of record identifiers. Different
  values per record need one `update` each.
- `batchdelete` moves a list of records to the trash like `delete` and is offered only by a connection whose
  `tools` list names it. Permanent deletion and restore have no batch form.
- `batchupdate` and `batchdelete` select their records only by the identifier list, sent as a filter on `id`.
  An empty, duplicated, malformed, or longer list is refused before any request, because Twenty acts on every
  record when the filter is missing.
- It is not established whether Twenty applies a batch as a whole when one record fails. An answer that does
  not name exactly the requested records (another count, a foreign or repeated identifier; for `upsert`,
  another count) is reported as an uncertain partial effect, not as success, and so is any unclear result.
  Read the records before repeating a batch.
- The answer is reduced like a read and capped; limits and error handling otherwise follow Writing records.
- No setup profile ticks the batch tools.

## Merging records

Duplicates and merges act on records of one reachable object; there is no merge across objects. The
identifiers are distinct UUIDs of that object.

- `records.duplicates` takes 1 to 20 identifiers and reports, for each, the possible duplicates by the
  criteria Twenty defines for the object, with their total count. It reads and changes nothing. Twenty
  answers only for records it finds, so an identifier that does not exist makes the whole call fail.
- `records.mergepreview` takes 2 to 9 identifiers and the position of the record that wins conflicts
  (`conflict_priority_index`, counted from 0). It shows the record the merge would leave and changes nothing;
  Twenty computes the preview in a dry run that no argument controls.
- `records.merge` takes the same arguments and merges for real: the other records are deleted and their
  relations are moved to the remaining record. Whether the deleted records can be restored is not promised.
  It is offered only by a connection whose `tools` list names it, requires confirmation, and no profile ticks
  it. Preview first, then merge.

Duplicates and results are records like those of `records.get` (depth 0, rich text as markdown, relations
only as identifier fields) and are untrusted workspace data. The answer of a merge must name one of the given
records. Qatlas sends each of these requests once; after an unclear result of `records.merge` it reports the
outcome as uncertain, and a 403 points to the object permissions of the role of the key.

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
deletion or adds a filter, so there is no deletion or restore by filter. A refused object is rejected before
the secret is read. Qatlas checks that the answer names the requested record. A 403 on
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
ticks `[read]` and the two company, two object, five record read tools, the link list tool, the three
workflow read tools, and the two member read tools; the profile `write` adds the two record write tools and
the link create tool. A profile is a visible starting selection, not a role: only the ticked `permissions` and
`tools` are saved, every tick can be changed before saving, and a saved connection never follows a profile.
