---
description: >
  Describes Twenty CRM company, object catalog, record read (list, get, structured search, search across
  objects, count by field), record write (create, update, batches), duplicate search and merge, record
  trash (delete, restore, destroy), note and task link operations, workflow read and control, member,
  role, and data model read and write, webhook operations, object targets, connection permissions, and safety
  boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-09
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace and, optionally, to a set of its
objects (see Object targets). It can list, read, create, change (name or primary domain), and delete
companies, and work with records of any reachable object: write, also up to 60 at a time (see Writing records
and Batches), delete, restore, destroy (see Deleting and restoring), find duplicates, merge (see Merging
records), and link notes and tasks (see Linking notes and tasks). It also covers webhooks (see Webhooks),
workflows (see Controlling workflows), and the data model (see Data model). Mutations require confirmation
and only use the generated REST routes of the company or of a reachable object, or fixed metadata, webhook,
and workflow routes. Qatlas sends each mutation once; after an unclear result (timeout, reset, server error,
unreadable answer) it reports the outcome as uncertain and does not repeat the request.

A `domain` value sets the primary link to `https://<domain>` with the domain as its label; an empty value
clears both. Twenty allows 100 requests per minute for each API key, and Qatlas spaces the requests of one
key accordingly.

## Object targets

A target has the form `object/NAME` with the singular camelCase API name of an object, for example
`object/person`. A connection may list several; a value without the `object/` form fails validation.

- With targets, the connection reaches exactly the bound objects.
- Without targets, the connection reaches every non-system object its API key reaches. Set `object/` targets
  on every connection that should not see all of them.
- System objects (messages, calendar events, attachments, workflows, workspace members, and the like) are
  never reachable through the object and record tools and cannot be a target; workflows and workspace
  members have their own read tools. The two link objects of notes and tasks are system objects too; only the
  link tools touch them. Qatlas keeps them in a fixed list, so an object a later Twenty version marks as a
  system object stays reachable on a connection without targets until the list is updated.
- `companies.*` need `company` to be reachable: with targets, `object/company` must be bound.
- Workspace-wide tools work only on a connection without object targets; on any other connection they are
  refused before the key is read.

`objects.list` and `objects.get` (`read`) show the reachable objects and their fields; relations to objects the
connection does not reach are left out. The catalog comes from the workspace's generated API document
(`/open-api/core`), which needs no settings right, unlike the metadata API (see Data model). Only
names, types, and flags leave Qatlas; descriptions and options of the document never do.

## Reading records

`records.list` and `records.get` (`read`) read the records of any reachable object, standard or custom, by its
singular API name. Qatlas takes the REST path of an object only from the workspace catalog, never from an
argument. A list returns one page without filters (see Filtering and counting records for conditions), 1 to
100 records, with an opaque `next_cursor`. `order_by` sorts by one field, or by `field.subfield` of a
composite field, in direction `asc` or `desc`. Relation, array, and rich-text fields cannot be sorted by.

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
- A value or an answer above the bounds is an invalid response; Qatlas does not cut it.
- Record values are untrusted workspace content, often personal data. They appear only in a result, never in
  errors, logs, or audit entries, and carry their own data class.
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
  conditions.
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

- `is` takes `NULL` or `NOT_NULL`; `in` takes a list of values of one type (see the tool help).
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
`records.get`. Qatlas sends one fixed query and passes the checked arguments only as variables; no argument
becomes part of a query, and there is no free GraphQL. Twenty reports a failed query as an answer with errors;
Qatlas maps it to a class and shows no text of it.

- `objects` limits the search to a subset of the reachable objects; an object outside the connection is
  ignored, and an empty remaining set is refused before the search is sent.
- The searched objects are always named explicitly: the bound objects with targets, otherwise the
  non-system objects of the workspace catalog, each cut with `objects`. Hits of any other object are
  dropped and only counted in `omitted`.
- The cursor is bound to the connection, its targets, `text`, and `objects`.

## Reading workflows

`workflows.list`, `workflows.get`, and `workflowruns.list` (`read`) are workspace-wide and let an agent follow
automations and find failed runs. `workflow`, `workflowVersion`, and `workflowRun` are system objects, so the
record tools never reach them; these tools use fixed routes and a fixed field selection.

- Step and trigger settings, run outputs, context, state, and error texts are never read out. Status,
  trigger, and step types come from fixed lists; any other value is shown as `unknown`.
- A workflow run cannot be started with an API key: Twenty refuses it, and Qatlas does not work around that.

## Controlling workflows

`workflowversions.activate`, `workflowversions.deactivate` (`update`), `workflowruns.stop`, and
`workflowruns.retry` (`execute`) are workspace-wide, need the settings right Workflows, and are offered only
by a connection whose `tools` list names them; no profile ticks them.

- `activate` reads the version first and activates it only if it opens no lasting path to the outside through
  Qatlas: a webhook trigger or a step that can send data out or run code is refused before any change. The
  check rests on trigger and step types alone, from a fixed positive list; activate other versions in Twenty.
  The version can change between the read and the activation.
- `retry` runs the steps of the run again, including steps with effects outside Twenty.

## Members and roles

`workspacemembers.list`, `workspacemembers.get`, and `roles.list` (`read`) are workspace-wide. Members are
personal data with their own data class; avatars, user identifiers, and member and key names are never read.

`roles.create`, `update`, `delete`, `setobjectpermissions`, `setfieldpermissions`, and `setpermissionflags` are
confirmed, need a `tools` entry, and assign nothing. They change only a role that a first read shows editable and
without API keys. A rights call covers one non-system object checked against the data model ("Data model"). Flags
are replaced as a whole. All need "Roles", so use a key of its own; no profile ticks them.

## Data model

`metaobjects.list`, `metaobjects.get`, and `metafields.get` (`read`) are workspace-wide and read Twenty's
metadata API for all objects and fields; default values, settings, and relation details are never read.

`metaobjects.create`, `metaobjects.update`, `metafields.create`, and `metafields.update` (`create`, `update`)
change the schema for every user and integration of the workspace. All seven tools need the Twenty right
"Data model", which also allows deleting the schema: use a connection with an API key of its own. The
writers are offered only by a connection whose `tools` list names them; no profile ticks any of them.

- Created are custom objects and non-relational fields; relation types are refused. A field is created on a
  custom object only; its options are required for selections and refused for other types.
- An update never changes API names or types; a read before the write refuses system and relation fields and
  limits standard objects and fields to label changes.
- Options are replaced as a whole: a left-out option clears its value in all records. Deactivating hides an
  object or field from everyone.
- A new custom object is reachable at once without targets; with object targets only after it is entered
  there, which Qatlas never does.

## Writing records

`records.create` and `records.update` (`create`, `update`) write one record of any reachable object. `create`
posts the given `fields`; `update` patches only the fields named for the record `id` and needs at least one.
Neither upserts or writes several records; see Batches. Qatlas takes the object, its route, and the shape of
every field from the workspace catalog and builds the request body from the checked values only.

- Every field is checked against the schema before the request: a `create` against the create shape of the
  object, with its required fields, an `update` against the update shape, without required fields. The
  schema read is the only other request, and a refusal after it sends no write. A refusal names neither
  field nor value.
- Supported are scalar fields, selections (values of the schema), and the composite fields emails, phones,
  links, currency, full name, and address; a composite value may contain only the parts the schema names.
  Null is refused; send an empty value to clear a text.
- Rich text takes only `{"markdown": ...}`, as a read reports it.
- A relation is set by its identifier field (`<relation>Id`, a UUID) and only when the relation's target is
  reachable through the connection; otherwise the field is refused without naming the target. Identifier
  fields that are not a relation to a reachable object are refused as well.
- Refused fields: system fields, fields the schema does not offer for the operation, files, actor fields,
  free JSON, and lists of plain text. The request size is bounded.
- The answer is the written record, read at depth 0 and reduced like a read; it must name the requested
  record.
- For `create` an uncertain report warns that repeating adds a duplicate record; search the object first.
- A 403 means the role of the API key lacks the right to write the object or a field.

## Linking notes and tasks

`activitytargets.list`, `activitytargets.create`, and `activitytargets.delete` (`read`, `create`, `delete`)
read, set, and remove the links between a note or a task and a record of any reachable object, standard or
custom. The argument `activity` (`note` or `task`) selects the link object. The agent names the object and the
record identifier; the field that carries the link comes from the workspace schema of the link object, never
from an argument. An object that the schema gives no single link relation cannot be linked.

- The note or task and the object must both be reachable through the connection, so with targets both
  `object/note` (or `object/task`) and the object are bound. A refusal comes before any secret is resolved
  and names neither object nor identifier.
- `list` names only links whose two sides are reachable and counts the others in `omitted`.
- Repeating `create` adds a duplicate link; the answer must name the requested link.
- `delete` removes only the link, never the note, task, or record. It reads the link first and removes it only
  when both sides are reachable. A connection offers it only when its `tools` list names it. Links are
  workspace data of the record data class.

## Webhooks

`webhooks.list`, `webhooks.get`, `webhooks.update`, and `webhooks.delete` (`read`, `read`, `update`, `delete`)
show where the workspace reports events, change which events it reports, and remove a webhook. There is no
tool to create a webhook or to change its target or signing key.

- The tools are workspace-wide. Twenty requires the settings right API keys and webhooks for the key; a 403
  points to it.
- The signing key is never shown, and the target is reduced to scheme, host with port, and path: query,
  fragment, and credentials are dropped, because they often carry tokens. The path stays visible, so webhook
  configuration has its own data class.
- `update` replaces the whole event list and/or the description. The input is checked before any request is
  sent, and the request contains nothing but these two fields.
- `delete` is offered only by a connection whose `tools` list names it.

## Batches

`records.batchcreate`, `records.batchupdate`, and `records.batchdelete` act on one reachable object with 1 to
60 records in one confirmed request that Qatlas sends once.

- `batchcreate` takes a list of records; each is checked like a `create`, and one refused record refuses the
  batch before anything is written. With `upsert` Twenty matches the entries against existing records by the
  unique fields of the object and changes the match instead of creating a record. Because `upsert` can change
  records, the tool carries the effect `update` and is not idempotent: a connection needs the `update`
  permission for it even without `upsert`.
- `batchupdate` sets one set of fields, checked like an `update`, on a list of record identifiers.
- `batchdelete` moves a list of records to the trash like `delete` and is offered only by a connection whose
  `tools` list names it. Permanent deletion and restore have no batch form.
- `batchupdate` and `batchdelete` select their records only by the identifier list, sent as a filter on `id`.
  An invalid list is refused before any request, because Twenty acts on every record without a filter.
- It is not established whether Twenty applies a batch as a whole when one record fails. An answer that does
  not name exactly the requested records (for `upsert`, another count) is reported as an uncertain partial
  effect, not as success, and so is any unclear result. Read the records before repeating a batch.
- The answer is reduced like a read and capped. No setup profile ticks the batch tools.

## Merging records

Duplicates and merges act on records of one reachable object; there is no merge across objects. The
identifiers are distinct UUIDs of that object.

- `records.duplicates` reports the possible duplicates of each given record by the criteria Twenty defines
  for the object and changes nothing. An identifier that does not exist makes the whole call fail.
- `records.mergepreview` shows the record a merge would leave and changes nothing; Twenty computes the
  preview in a dry run that no argument controls.
- `records.merge` takes the arguments of the preview and merges for real: the other records are deleted and
  their relations are moved to the remaining record. Whether the deleted records can be restored is not
  promised. It is offered only by a connection whose `tools` list names it, and no profile ticks it. Preview
  first, then merge.

Results are untrusted records like those of `records.get`; a merge answer must name one of the given records.
A 403 points to the object permissions of the role of the key.

## Deleting and restoring

Companies and records of any reachable object share one trash contract:

- `delete` moves one company or record to Twenty's trash. It stays recoverable.
- `restore` brings one company or record back from the trash.
- `destroy` deletes one company or record permanently. It cannot be undone, and `restore` cannot bring it
  back. Twenty's answer does not tell soft from permanent deletion, so the result only confirms that Twenty
  accepted the request.
- `list` with `deleted` set to `true` lists only what is in the trash; this holds for `companies.list` and
  `records.list`, not for `records.search`.

`records.delete`, `records.restore`, and `records.destroy` each act on one record of one reachable object,
identified by its UUID, and send one fixed route; no argument switches `delete` to permanent deletion or adds
a filter, so there is no deletion or restore by filter. A refused object is rejected before the secret is
read, and the answer must name the requested record. A 403 on `delete` and `destroy` points to the right of
the role of the key (Delete Records, Destroy Records); Twenty's own text is never shown.

`delete` and `destroy`, for companies and records, are offered only by a connection whose `tools` list names
them; `permissions` alone does not admit them. No profile ticks them or `records.restore`.

The credential provides `api-key`. The key's workspace role remains the provider-side ceiling; the
connection's local `permissions` list can only narrow it, and an optional `tools` list, for example
`[twentycrm.companies.get]`, narrows it further to named tools without admitting an effect `permissions`
excludes. The `companies.*` tools expose conservative core company fields and accept no custom-field
payloads; custom fields are written through `records.create` and `records.update`. Invocation arguments never
replace the configured origin. The terminal editor starts a new connection on the setup profile `read`, which
ticks `[read]` and the read tools it lists; the profile `write` adds the two record write tools and the link
create tool. A profile is a visible starting selection, not a role: only the ticked `permissions` and
`tools` are saved, every tick can be changed before saving, and a saved connection never follows a
profile.
