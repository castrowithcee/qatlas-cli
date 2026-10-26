---
description: >
  Describes the Make data store, data structure, and data store record tools: the team binding, deleting
  stores, and the bounds and key rules of
  records.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Make: data stores and records

Errors follow [Errors](README.md#errors).

## Data stores and data structures

`make.data-stores.*` manage the data stores (tables scenarios keep data in) of the bound team; the records inside
a store are not touched. `make.data-structures.list` and `make.data-structures.get` read the field layouts stores
follow; creating, changing, or deleting a structure is not offered. A Qatlas connection with a scenario
allow-list reaches none of them: they belong to no scenario, so the narrower reading is refusal, locally and
before any secret is read.

The token scopes, including the scope Make's reference lists for reading one store, are under
[Data stores and data structures](setup.md#data-stores-and-data-structures).

Team binding. The teamId of list and create is always the connection's bound team, never an argument. Every
`datastore_id` and `datastructure_id` is read from Make first and refused as an invalid request, without naming
its real team, when its `teamId` is not the bound team; this happens before any change, so a `datastructure_id`
of another team in a create or update sends no changing request. Data stores and structures report no
organization, so a configured organization target is not checked against them.

Fields are typed and bounded: `name` 1 to 128 characters without control characters (it need not be unique),
`max_size_mb` an integer from 1 to 1048576 (the upper bound is a local ceiling, Make documents none), and an
update needs at least one of `name`, `datastructure_id`, `max_size_mb`. Only allow-listed fields are returned
(`id`, `name`, `team_id`, `records`, `size`, `max_size`, `datastructure_id`; strings capped at 256 characters;
size values are decoded as text or number). A structure's specification is returned by `data-structures.list`,
`data-structures.get`, and `data-store-records.list` with at most 200 fields and 4 levels and 128 characters per
string. The default of a boolean, text, or number field is returned as a value of that type, numbers as the exact
literal Make sent. A default that has another type than its field, cannot be read, or is longer than 256
characters is left out, never cut, and `spec_truncated` marks it like any other omitted part; the other field
types have no documented default. All of it is untrusted data. Lists return at most 200 entries per page, sorted
by name; `sort_dir` only sets the direction (`asc` when omitted), because `name` is the one sort column Make
documents.

`make.data-stores.delete` reads and binds the store, then sends one `DELETE /data-stores` with the bound `teamId`
and exactly one id; the delete-all form is never used. Make requires `confirmed=true` when a scenario includes
the store and otherwise refuses and deletes nothing. Without `confirm_scenarios_affected` the tool never sends
`confirmed`: when Make refuses and names scenarios, it returns `confirmation_required` with those ids and names
(at most 20, names capped) and repeats nothing; a refusal naming no scenario is an ordinary error. With
`confirm_scenarios_affected` true it sends `confirmed=true`, the store is deleted with its records, and the
scenarios using it stop working. The tool is in no profile and is offered only when a connection's `tools` list
names it. The profiles are `datastores-read`, `datastores-manage` (reads plus create and update), and
`datastructures-read`. Create, update, and delete need their own confirmation, send exactly one changing
request after the reads, and are never retried; a timeout, a connection reset, a 5xx, or an unreadable answer is
reported as uncertain, and a result outside the bound team after the change as a `provider-error`.

## Data store records

`make.data-store-records.list`, `.create`, `.replace`, `.update`, and `.delete` work on the records of one data
store of the bound team. Every `datastore_id` is read and bound to the bound team first (see
[above](#data-stores-and-data-structures)), before the list or the one changing request; a store of another team is
refused without naming it. A connection with a scenario allow-list reaches none of them. The token scopes are in
the same section of [Set up Make](setup.md#data-stores-and-data-structures).

Make reports the store's specification and `strict` flag with every page of records, and both are returned like
those of a data structure. `total_count` is Make's own count of the records in the store and `count` stays the
number of records on the page. Records have no sorting argument: Make documents no sorting for records.

Source: developers.make.com API reference, Data stores, Data (checked against the published reference, not a
live account). Record contents are untrusted user data and likely personal data (sensitivity
`make-data-store-records-personal-data`). A page holds at most 50 records (20 by default); each record's data is
capped to 8 KiB, 6 levels, 200 nodes, and 256 characters per string, with `truncated` set when anything was cut.
`create` takes `data` as a JSON object of at most 64 KiB and 16 levels (a local ceiling; Make validates it
against the store's data structure) and an optional `key` of 1 to 128 characters (letters, digits, `_`, `.`,
`:`, `-`, not starting with a dot or hyphen; Make documents no key format, so this is the narrower local form);
without a key Make generates one. `delete` takes 1 to 50 distinct keys of the same form and sends only the
`keys` form: Make's `all`, `exceptKeys`, and `confirmed` are never sent. It reports the requested keys Make
names as deleted. It is in no profile and is offered only when a connection's `tools` list names it. The profiles
are `datastorerecords-read` and `datastorerecords-manage` (list, create, replace, and update). Changes need their
own confirmation, send exactly one changing request, and are never retried; a timeout, a connection reset, a 5xx,
or an unreadable answer is reported as uncertain.

`replace` and `update` require `key` and a `data` object with at least one property (same ceilings as `create`)
and send it directly as the body, without a `{"data":...}` wrapper; the reference publishes no request
example, so this form follows Make's official TypeScript SDK. `replace` leaves exactly the sent fields, `update`
keeps unnamed top-level fields. Whether Make creates the record for an unknown key is not verified.
