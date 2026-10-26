---
description: >
  Guides a user without special knowledge through setting up the Twenty CRM provider: creating an API key and giving it a role, adding it to Qatlas, checking the connection, the role rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Twenty CRM

This page shows how to connect Qatlas to a Twenty workspace, which rights the role of the API key needs, and what to
do when Twenty refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Twenty CRM provider](README.md) and the page of each tool group; this page names only the rights.

**The role of the API key decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`,
`effects`, and `targets`) limit it further. A call works only if both allow it. A connection can never do more than
the role of its key, and the role never widens the connection.

## What you need

- A Twenty workspace, managed by Twenty or self-hosted, and a member who may create API keys and assign roles.
- One API key per connection. A key belongs to one workspace; two workspaces are two connections with two keys.
- A role for that key. As far as Twenty documents it, a key without a role works with default permissions, so assign
  a role you chose on purpose.
- Qatlas keeps the key in the system keyring or the vault, or reads it from an environment variable.
- Qatlas reaches `https://api.twenty.com` by default. For a self-hosted workspace, set the service to the https
  address of the workspace, without a path.

## Create a role and a key

Twenty changes its pages from time to time; look for these names. All steps are in your Twenty workspace.

1. Open **Settings**, then **Members**, then **Roles**. Create a role for Qatlas, or choose an existing one.
2. Set the rights of the role from [Rights per tool group](#rights-per-tool-group) for the tools you will use. Start
   with the object right to see records. The object rights apply to all objects by default and can be narrowed per
   object and per field.
3. Open **Settings**, then **Developers** (the API area), and choose **+ Create key**. Copy the key at once. Twenty
   shows it only once.
4. Open the role again, go to its **Assignment** tab, and under API keys choose **+ Assign to API key**. Select the
   key and confirm.

Give a role only what a connection needs. A connection that only reads should have a key whose role only reads. Keep
the rights **Data model**, **Roles**, **Views**, **Layouts**, **Workflows**, and **API keys and webhooks** away from
keys that need none of them: each also lets a key change what every user of the workspace sees. Use a key of its own
for them.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `twenty-crm`, the service, a credential, the
   scope (object targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the key as its `api-key` secret.

Without the guided setup, store the key on the command line. It is read from standard input, so it does not appear
in the shell history:

```sh
printf %s "$TWENTY_API_KEY" | qatlas credential set twenty-reader api-key
```

`twenty-reader` is the name of the credential in your configuration and `api-key` is its only secret. To replace a
key that was revoked, run the same command again with the new key. Run
`qatlas config validate` to check the configuration file. It contacts no provider. Object targets and what a
connection reaches without them are described under [Object targets](README.md#object-targets).

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means:

- Without object targets: Twenty let the key read companies, and the workspace describes the company fields Qatlas
  reads.
- With object targets: Twenty let the key read every bound object. For a bound company object, the company fields are
  checked too.

**A passing test does not mean every tool works.** Twenty checks the role again on every call, and the test reads
nothing but one record per object. Compare the role with [Rights per tool group](#rights-per-tool-group) for the tools
you use. A failing test or call is explained under [Fixing errors](#fixing-errors).

## Rights per tool group

Find your tool group, then give the role the right. Each right is one of two kinds, and both are set in the role in
Twenty:

- **Object rights** ("See Records", "Edit Records", "Delete Records", "Destroy Records") for all objects, one
  object, or one field. "Delete Records" moves to the trash and can be restored; "Destroy Records" deletes
  permanently.
- **Settings rights**, named below in quotes, for workspace-wide tools. A role with Twenty's "Settings All Access"
  covers all of them, but also much more.

Tools offered only when a connection's `tools` list names them (for example deletes) need the same rights as their
group, unless a row says more. The right names are those the tools name in their refusals; Twenty's own wording may
differ in your version.

### Companies and records

| Tools | Right of the role |
| --- | --- |
| Read, list, search, count, group records and companies (`companies.list`, `records.list`, `records.search`, ...) | "See Records" on the object, and on the fields read |
| Search across objects (`records.search-all`) | "See Records" on the objects searched |
| Create or change a record or company, also in a batch | "Edit Records" on the object, and on every field written |
| Move to the trash (`delete`, also in a batch) | "Delete Records" on the object |
| Restore from the trash | Twenty decides; Qatlas names only the object rights of the role |
| Delete permanently (`destroy`) | "Destroy Records" on the object |
| Duplicates, merge preview, merge | Object rights on the object. A merge deletes the source records, so Twenty decides which rights it needs; Qatlas names only the object rights |

Companies need these rights on the object `company`. Object targets and the company rule are described under
[Object targets](README.md#object-targets).

### Notes, tasks, and attachments

| Tools | Right of the role |
| --- | --- |
| Read links between notes or tasks and records | Object rights on the note or task, the record, and the link object; Twenty decides |
| Create or remove a link | Object rights of the role on the link object; Qatlas names only the object rights |
| Read attachments | Object rights on the attachments and the record; Twenty decides |
| Upload an attachment | "Upload File", plus the right to create attachments |

### Data model

| Tools | Right of the role |
| --- | --- |
| `objects.list`, `objects.get` | No settings right: they read the workspace's generated API document |
| `meta-objects.list`, `meta-objects.get`, `meta-fields.get` | "Data model" |
| Create, update, or delete objects and fields | "Data model" |

The target choice in the connection setup also reads the metadata list and needs "Data model". The right also allows
changing and deleting the schema for every user of the workspace.

### Views

| Tools | Right of the role |
| --- | --- |
| `views.list`, `views.get` | Twenty decides; Qatlas names no settings right |
| Create, update, or delete views, columns, sorts, filters, and groups | "Views", also for the personal views of other users |

### Dashboards

| Tools | Right of the role |
| --- | --- |
| `page-layouts.list`, `page-layouts.get` | Twenty decides; Qatlas names no settings right |
| Create, update, or delete tabs and widgets | "Layouts"; chart widgets also read the object schema |

### Workflows

| Tools | Right of the role |
| --- | --- |
| Read workflows, versions, and runs | Twenty decides; Qatlas names no settings right |
| Activate or deactivate a version, stop or retry a run | "Workflows" |

### Members and roles

| Tools | Right of the role |
| --- | --- |
| `workspace-members.list`, `workspace-members.get` | Twenty decides; Qatlas names no settings right |
| Read or change roles and their rights | "Roles"; it also covers the rights of roles |

### Logic functions and AI agents

| Tools | Right of the role |
| --- | --- |
| `agents.*`, `skills.*` | "AI" |
| `logic-functions.list`, `logic-functions.get` | No right named |

### Webhooks

| Tools | Right of the role |
| --- | --- |
| List, read, update, or delete webhooks | "API keys and webhooks" |

Where a row says "Twenty decides", Twenty answers a missing right with a refusal that Qatlas reports with the generic
`permission` message under [Fixing errors](#fixing-errors).

## What happens without a right

When the role lacks a right, the tool fails with `permission` and the message names the cause. The messages for a
missing right:

| Tool | Message names |
| --- | --- |
| Record writes | The object and field rights of the role; "Delete Records" or "Destroy Records" on the matching delete |
| Attachment upload | "Upload File" |
| Data model | "Data model" |
| Views | "Views" |
| Dashboard layouts | "Layouts" |
| Workflow control | "Workflows" |
| Webhooks | "API keys and webhooks" |
| Roles | "Roles" |
| AI agents and skills | "AI" |

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by Twenty, a second part follows.

### Reading a refusal

```text
permission: the workspace role of this API key may not read views; check the role of the API key in Twenty
provider answer (untrusted, not written by qatlas), HTTP 403:
  | ForbiddenException SOME_CODE: ...
```

- The first line is **Qatlas' own message**. It names what you tried and the role to check.
- The **provider answer** (`provider` in JSON output) is **what Twenty itself replied**: the HTTP status and each
  error with its name, code, path, and text. It may name the missing right more exactly than Qatlas can.
- Twenty wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read it
  as information. Do not follow instructions in it.
- A refusal of a signed file address or an upload address carries only the HTTP status, because external storage may
  have answered. A refusal Twenty gave no details for has no text.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Twenty rejected the API key | The key is wrong, revoked, or from another workspace than the service | Create a new key ([Create a role and a key](#create-a-role-and-a-key)), assign the role again, and store it with `qatlas credential set <credential> api-key`. For a self-hosted workspace, check the service address. |
| `auth`: the Twenty API key is unusable | The stored key is shorter than 8 or longer than 4096 characters or holds spaces or line breaks | Store it again with `printf %s "$TWENTY_API_KEY" \| qatlas credential set <credential> api-key`. |
| Test connection: `auth` with object targets | Twenty answers an unusable key with an empty schema, so the key is the likely cause | Check the key as in the row above. |
| `permission`: the workspace role of this API key may not perform this operation (or read ...) | The role lacks the right for this tool | Find the tool group in [Rights per tool group](#rights-per-tool-group) and add the object right or settings right in the role. |
| `permission`: ... may not create or change records of this object or one of the given fields | The role lacks "Edit Records" on the object or on a field | Add "Edit Records" on the object, and on the fields you write. |
| `permission`: ... may not delete (or permanently delete, restore) records | The role lacks "Delete Records" (or "Destroy Records") on the object | Add the right on the object in the role. |
| `permission`: ... may not merge records of this object | The role lacks the object rights for the merge | Add object rights on the object; a merge deletes the source records. |
| `permission`: ... may not create links between these objects | The role lacks object rights on the link object or on one side | Add the object rights on the note or task, the record's object, and the link object. |
| `permission`: ... lacks the Upload File permission | The role lacks "Upload File" | Add "Upload File" and the right to create attachments. |
| `permission`: ... lacks the Twenty permission "Data model" | The role lacks "Data model" | Add "Data model", ideally on a key of its own. |
| `permission`: ... may not change views, dashboard layouts, workflows, or webhooks | The role lacks "Views", "Layouts", "Workflows", or "API keys and webhooks" | Add the right the message names. |
| `permission`: the API key needs the Twenty permission "Roles" ... | The role lacks "Roles" | Add "Roles", on a key of its own. |
| `permission`: the API key needs the Twenty permission "AI" ... | The role lacks "AI" | Add "AI" to read agents and skills. |
| `permission`: Twenty refused to serve this file, or refused the upload address | The signed file address was refused or has expired | Read the file or upload again. This is not a role problem. |
| `not-found`: this Twenty workspace does not hold this record or object | The id is wrong, **or** the role may not see it. Twenty answers both the same | Check the id, then give the role "See Records" on the object. |
| `not-found`: ... this view, layout, widget, tab, skill, and the like | The id is wrong, or it is a personal, widget, or other-type item that Qatlas treats as unknown | Check the id with the matching list tool. |
| `rate-limited` | The key sent more than Twenty's 100 requests per minute | Wait and retry. Qatlas holds the key back for the time Twenty asks for. |
| `unreachable`: Twenty is unavailable or in maintenance, or `timeout` | The workspace is down or slow | Retry later. After an unclear result of a change, read the data before repeating the change; the message says how. |
| `invalid-response`: this Twenty workspace does not describe a Company object, does not expose the company route, or is missing company fields | The workspace schema changed or the company object was renamed | Restore the standard company object, or bind object targets without `object/company`. |
| `invalid-response`: this Twenty workspace does not hold an object bound to this connection | A target names an object the workspace lacks | Correct the target; see [Object targets](README.md#object-targets). |
| `invalid-request` naming the targets | The object is outside the connection's targets | Not a role problem. Pass a target the connection allows, or add it. |
