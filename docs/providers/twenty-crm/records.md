---
description: >
  Describes the Twenty CRM record tools: reading, filtering and counting, writing, batches, merging, and deleting and restoring records.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Twenty CRM: records

## Reading records

`records.list` and `records.get` (`read`) read the records of any reachable object, standard or custom, by its
singular API name. Qatlas takes the REST path of an object only from the workspace catalog, never from an argument.
A list returns one page without filters (see [Filtering and counting records](#filtering-and-counting-records) for
conditions), with an opaque `next_cursor`, sorted by one field or `field.subfield` of a composite field. Relation,
array, and rich-text fields cannot be sorted by.

- Qatlas checks `fields` and `order_by` against the response shape of the object in the workspace document,
  so an unknown field is refused before any record is read, and the refusal does not name it.
- Qatlas always reads at depth 0: a relation appears only as its identifier field (`companyId`), never as an
  embedded record, and a relation to an object the connection cannot reach stays an identifier. Rich text
  appears only as `markdown`.
- Twenty signs file addresses with a token that serves the content without sign-in, so record values never
  show a file address; read a file through `twenty-crm.attachments.*`, which never output the token.
- A cursor is bound to the connection, object, sort, and field selection that produced it; any other use is
  refused before a request is sent.
- A value or an answer above the bounds is an invalid response; Qatlas does not cut it.
- `views.list` and `page-layouts.list` report no `total_count`, because Qatlas filters those lists locally by
  visibility and type.
- Record values are untrusted workspace content, often personal data. They appear only in a result, never in
  errors, logs, or audit entries, and carry their own data class.

## Filtering and counting records

`records.search` and `records.group-by` (`read`) take no filter expression. The agent gives structured
conditions and Qatlas builds Twenty's filter from checked parts only: Twenty's grammar has no escaping, so no
value may carry a quote, bracket, colon, comma, backslash, parenthesis, or percent sign.

- `records.search` reads a page like `records.list` for 1 to 5 AND-linked conditions
  `{field, operator, value}`. The cursor is also bound to the normalized conditions, whose order and repeats
  do not matter.
- `records.group-by` counts the records per value of 1 or 2 different fields, optionally limited by the same
  conditions.
- A condition field is a top-level field of the object's response shape or `field.subfield` of a composite
  field, only when the schema names the subfield. Relation fields, `deletedAt`, rich text, arrays and
  multi-selections, and subfields that hold identifiers are not filterable. Soft-deleted records are never
  searched.
- A relation identifier (`<relation>Id`) is accepted only when the relation field names a target object that
  the connection reaches; otherwise the condition could probe objects outside the targets.
- The operators follow the field kind, which Qatlas derives from the schema; the tool help lists them. A
  selection value must be an enum value of the schema.
- A refused field, operator, or value is refused after the schema was read and before any record is read,
  and the refusal names neither field nor value. The enum values of the schema serve this check.
- `records.group-by` groups by selections, booleans, dates (by day, UTC), and relation identifiers, not by text,
  numbers, composite subfields, or the record `id`. Twenty cuts a longer group list silently, so an answer
  with 200 or more groups is refused as possibly incomplete; narrow it with conditions. Group values are
  untrusted workspace data of the same data class as records.

## Writing records

`records.create` and `records.update` (`create`, `update`) write one record of any reachable object. `create`
posts the given `fields`; `update` patches only the fields named for the record `id` and needs at least one.
Neither upserts or writes several records; see [Batches](#batches). Qatlas takes the object, its route, and the shape of
every field from the workspace catalog and builds the request body from the checked values only.

- Every field is checked against the schema before the request: a `create` against the create shape of the
  object, with its required fields, an `update` against the update shape, without required fields. The
  schema read is the only other request, and a refusal after it sends no write. A refusal names neither
  field nor value.
- Supported are scalar fields, selections (values of the schema), and the composite fields emails, phones,
  links, currency, full name, and address; a composite value may contain only the parts the schema names.
  Null is refused; send an empty value to clear a text. Rich text takes only `{"markdown": ...}`, as a read
  reports it.
- A relation is set by its identifier field (`<relation>Id`, a UUID) and only when the relation's target is
  reachable through the connection; otherwise the field is refused without naming the target. Identifier
  fields that are not a relation to a reachable object are refused as well.
- Refused fields: system fields, fields the schema does not offer for the operation, files, actor fields,
  free JSON, and lists of plain text. The request size is bounded.
- The answer is the written record, reduced like a read; it must name the requested record.
- For `create` an uncertain report warns that repeating adds a duplicate record; search the object first.

## Batches

`records.batch-create`, `records.batch-update`, and `records.batch-delete` act on one reachable object in one
confirmed request that Qatlas sends once; no profile ticks them.

- `batch-create` takes a list of records; each is checked like a `create`, and one refused record refuses the
  batch before anything is written. With `upsert` Twenty matches the entries against existing records by the
  unique fields of the object and changes the match instead of creating a record. Because `upsert` can change
  records, the tool carries the effect `update` and is not idempotent: a connection needs the `update`
  permission for it even without `upsert`.
- `batch-update` sets one set of fields, checked like an `update`, on a list of record identifiers.
- `batch-delete` moves a list of records to the trash like `delete` and is offered only by a connection whose
  `tools` list names it. Permanent deletion and restore have no batch form.
- `batch-update` and `batch-delete` select their records only by the identifier list, sent as a filter on `id`.
  An invalid list is refused before any request, because Twenty acts on every record without a filter.
- It is not established whether Twenty applies a batch as a whole when one record fails. An unclear result, or an
  answer that does not name exactly the requested records (for `upsert`, another count), is reported as an
  uncertain partial effect, not as success. Read the records before repeating a batch.

## Merging records

Duplicates and merges act on records of one reachable object; there is no merge across objects. The
identifiers are distinct UUIDs of that object.

- `records.duplicates` reports the possible duplicates of each given record by the criteria Twenty defines
  for the object and changes nothing. An identifier that does not exist makes the whole call fail.
- `records.merge-preview` shows the record a merge would leave and changes nothing; Twenty computes the
  preview in a dry run that no argument controls.
- `records.merge` takes the arguments of the preview and merges for real: the other records are deleted and
  their relations are moved to the remaining record. Whether the deleted records can be restored is not
  promised. It is offered only by a connection whose `tools` list names it, and no profile ticks it.
- Results are untrusted records; a merge answer must name one of the given records (rights:
  [Companies and records](setup.md#companies-and-records)).

## Deleting and restoring

Companies and records of any reachable object share one trash contract:

- `delete` moves one company or record to Twenty's trash. It stays recoverable.
- `restore` brings one company or record back from the trash.
- `destroy` deletes one company or record permanently. It cannot be undone or restored. Twenty's answer
  does not tell soft from permanent deletion, so the result only confirms that Twenty accepted the request.
- `list` with `deleted` set to `true` lists only what is in the trash; this holds for `companies.list` and
  `records.list`, not for `records.search`.

`records.delete`, `records.restore`, and `records.destroy` each act on one record of one reachable object,
identified by its UUID, and send one fixed route; no argument switches `delete` to permanent deletion or adds
a filter, so there is no deletion or restore by filter. A refused object is rejected before the secret is
read, and the answer must name the requested record. Twenty's own text is never shown (rights:
[Companies and records](setup.md#companies-and-records)). `delete` and
`destroy`, for companies and records, are offered only by a connection whose `tools` list names them;
a connection that selects by `effects` does not admit them. No profile ticks them or `records.restore`.
