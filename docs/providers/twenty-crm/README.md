---
description: >
  Describes Twenty CRM company, object catalog, record read, write, duplicate and merge, and trash
  operations, note and task links, attachment read, download, and upload, workflow read and control, member,
  role, AI agent, skill, and logic function, view, dashboard, and data model read and write, webhooks,
  object targets, connection effects, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-22
---

# Twenty CRM

A connection binds one API key to one managed or self-hosted workspace and, optionally, to a set of its
objects (see [Object targets](#object-targets)). It can list, read, create, change (name or primary domain), and delete
companies, and work with records of any reachable object, also up to 60 at a time, plus notes, tasks,
attachments, webhooks, workflows, dashboards, and the data model; each has its own page. Mutations require
confirmation and only use the generated REST routes of the company or of a reachable object, or fixed
metadata, webhook, and workflow routes. Qatlas sends each mutation once; after an unclear result (timeout,
reset, server error, unreadable answer) it reports the outcome as uncertain and does not repeat it. Requests
of one key are spaced to Twenty's limit of 100 per minute.

A `domain` sets the primary link `https://<domain>` with the domain as label; an empty value clears both.

Pages by area:

- [Records](records.md): reading, filtering and counting, writing, batches, merging, deleting and restoring
- [Searching across objects](search.md)
- [Workflows](workflows.md)
- [Members and roles](members-and-roles.md)
- [Logic functions and AI agents](logic-and-ai.md)
- [Data model](data-model.md)
- [Views](views.md)
- [Dashboards](dashboards.md)
- [Notes, tasks, and attachments](activity-and-attachments.md)
- [Webhooks](webhooks.md)

## Object targets

A target has the form `object/NAME` with the singular camelCase API name of an object, for example
`object/person`. A connection may list several; a value without the `object/` form fails validation.

- With targets, the connection reaches exactly the bound objects.
- Without targets, the connection reaches every non-system object its API key reaches. Set `object/` targets
  on every connection that should not see all of them.
- System objects (messages, calendar events, attachments, workflows, workspace members, and the like) are
  never reachable through the object and record tools and cannot be a target; workflows, workspace
  members, and attachments have their own tools. The two link objects of notes and tasks are system
  objects too; only the link tools touch them. Qatlas keeps them in a fixed list, so an object a later
  Twenty version marks as a system object stays reachable on a connection without targets until the list
  is updated.
- `companies.*` need `company` to be reachable: with targets, `object/company` must be bound.
- Workspace-wide tools work only on a connection without object targets; on any other connection they are
  refused before the key is read and listed as not reachable.

The target choice lists the objects from one page of the metadata list (`/rest/metadata/objects`, the Data
model right, see [Data model](data-model.md)) as `object/NAME` with the label of the object; the list ignores a connection's
targets, so the choice also offers objects outside them, and system objects are left out. The metadata API has
no search, so the list is filtered locally. Qatlas resolves no URL to a target.

`objects.list` and `objects.get` (`read`) show the reachable objects and their fields; relations to objects the
connection does not reach are left out. The catalog comes from the generated API document, which needs no
settings right, unlike the metadata API (see [Data model](data-model.md)). Only names, types, and flags leave Qatlas.

## Credential and profiles

The credential provides `api-key`. The key's workspace role remains the provider-side ceiling; the
connection's local selection can only narrow it, by `effects` or by a `tools` list naming exactly the tools it
offers. The `companies.*` read tools report the whole company record, own fields under `custom_fields`; they
read no workspace catalog, so a file list, rich text, or signed file address is recognized by its shape, and a
value beyond the size limits is left out and named in `omitted_fields`. `addressOld` of a company is marked
deprecated in Twenty and is left out. Own fields are written through `records.create` and `records.update`.
Invocation arguments never replace the configured origin. The terminal editor starts a new connection on the setup profile `read`.
A profile is a visible starting selection, not a role: only the ticked `tools` are saved,
and a saved connection never follows a profile.
