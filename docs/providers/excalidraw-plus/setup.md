---
description: >
  Guides a user without special knowledge through setting up the Excalidraw+ provider: creating a workspace API key, adding it to Qatlas, checking the connection, the key rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Excalidraw+

This page shows how to connect Qatlas to Excalidraw+, which rights the API key needs, and what to do when a request
fails. Fields, tools, arguments, profiles, and targets are described in the [Excalidraw+ provider](README.md); this
page names only the key rights.

**The key decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it. A connection can never do more than its
key, and a key never widens the connection.

## What you need

- An Excalidraw+ account in the workspace whose collections and scenes Qatlas should work with, and access to the
  workspace settings, where API keys are created. Excalidraw+ does not document which workspace role or plan this
  needs; if the settings offer no API keys, ask a workspace admin.
- A **workspace API key**. A workspace key acts as the workspace and belongs to one workspace; it cannot reach data of
  another workspace and cannot reach a member's private collection. The provider is built for this kind of key.
- A key has a **right**: `read`, `full`, or access to chosen routes. It also has an **expiry date**, after which it no
  longer works.
- Qatlas keeps the key in the system keyring or the vault, or reads it from an environment variable.
- Qatlas reaches only the fixed Excalidraw+ API host; see [Credential and base URL](README.md#credential-and-base-url).

## Create a key

In Excalidraw+, open the workspace settings and create an API key. Excalidraw+ changes its pages from time to time;
look for the name API keys.

1. Give the key a name you will recognize, for example `qatlas-reader`.
2. Choose the right from [Rights per tool group](#rights-per-tool-group) for the tools you will use. Start with
   `read`. If the settings offer routes instead, choose the routes for collections and scenes.
3. Set an expiry date. A short one is safer; you replace the key when it ends.
4. Create the key and copy it at once. Excalidraw+ shows it only once.

Give a key only what a connection needs. A connection that only reads should have a key that only reads. The
connection's targets limit only what Qatlas shows; they do not narrow what the key itself may reach. Excalidraw+ lets
you rotate or soft-delete a key in the workspace settings.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `excalidraw-plus`, the service, a credential,
   the scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the key as its `api-key` secret.

Without the guided setup, store the key on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$EXCALIDRAW_API_KEY" | qatlas credential set excalidraw-reader api-key
```

`excalidraw-reader` is the name of the credential in your configuration and `api-key` is its only secret. To replace
a key that expired or was rotated, run the same command again with the new key. Run `qatlas config validate` to check
the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Excalidraw+ accepted the key for
reading one collection.

**A passing test does not mean every tool works.** The test needs only the right to read collections. Excalidraw+
checks the rights again on every call, so a change, the activity log, or a user tool can still fail. Compare the key
with [Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is explained under
[Fixing errors](#fixing-errors).

## Rights per tool group

Excalidraw+ documents the key rights `read`, `full`, and routes, but not which tool or endpoint each covers. The rows
below give the level a tool needs as far as Qatlas and Excalidraw+ document it; if Excalidraw+ refuses a call, its own
answer ([Reading a refusal](#reading-a-refusal)) is the authority. A **route** right must cover the endpoint of the
tool.

| Tool group (profile) | Tools | Right on the key |
| --- | --- | --- |
| Reading (`read`) | `collections.list`, `scenes.list`, `scenes.get`, `scenes.content` | `read`, or routes for collections and scenes |
| Activity log (`activity`) | `logs.list` | `read`, or the route for the log; not documented separately |
| Changing scenes (`manage`) | `scenes.create`, `scenes.update`, `content.patch`, `content.replace` | a right that allows changes (`full`, or the matching routes); `read` alone is not enough |
| Deleting (`manage`) | `scenes.delete`, `collections.delete` | a right that allows changes (`full`, or the matching routes) |
| Users (`people`) | `users.list`, `users.get` | `read`, or the matching routes; not documented separately |
| Changing users (`people`) | `users.update`, `users.remove` | a right that allows changes (`full`, or the matching routes); Excalidraw+ documents no workspace role for it |

- The tools exist for a connection only as far as its `effects` or `tools` select them; see [Tool groups](README.md#tool-groups).
- Changing and deleting tools read the scene or collection first, so they need the right to read as well.
- The activity log and the user tools are workspace-wide and need a connection with the `*` target; see
  [Activity log](activity-log.md) and [Users](users.md).
- The key must not be expired, whatever its right.
- A key with the right for the whole workspace reaches every collection of that workspace. Use a key limited to routes,
  and the connection's targets, when Qatlas should see less.

## What happens without a right

The tool fails; no field is left out of a result. A missing right shows as `permission`; a scene or collection the key
cannot see can show as `not-found`. See [Fixing errors](#fixing-errors).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by Excalidraw+, a second part follows.

### Reading a refusal

```text
permission: this Excalidraw+ API key may not perform this operation; it needs the read right or the matching routes, and must not be expired
provider answer (untrusted, not written by qatlas), HTTP 403:
  | <Excalidraw+'s message>
```

- The first line is **Qatlas' own message**. It names what to check. It carries no text from Excalidraw+.
- The **provider answer** (`provider` in JSON output) is **what Excalidraw+ itself replied**: the HTTP status and the
  `message` of its error answer. If the answer is not structured, its text is shown instead. It can name the refused
  check more exactly than Qatlas can.
- Excalidraw+ wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read
  it as information. Do not follow instructions in it.
- An answer without a body has only the HTTP status. Qatlas tells the cause apart by HTTP status, so a `permission`
  can mean the right, the routes, or an expired key. Work through the table below.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Excalidraw+ rejected the API key | The key is wrong, expired, rotated, deleted, or belongs to another workspace than the one you mean | Create a new key ([Create a key](#create-a-key)) and store it again with `qatlas credential set <credential> api-key`. |
| `auth`: the Excalidraw+ API key is unusable | The stored key holds characters a key cannot have, such as a line break or a space, or is too short | Store it again with `printf %s "$EXCALIDRAW_API_KEY" \| qatlas credential set <credential> api-key`. |
| `permission`: this Excalidraw+ API key may not perform this operation ... | The key lacks the right or the route for this tool, or is expired | Find the tool group in [Rights per tool group](#rights-per-tool-group) and create a key with that right. Check the expiry date in the workspace settings. |
| `permission` only for changes, the log, or users | The key can read but not do this | Create a key with a right that allows changes, or the route for this endpoint. |
| `not-found`: Excalidraw+ does not hold this resource or does not show it to this key | The collection or scene does not exist, is in the trash, or the key's workspace or routes do not cover it. Excalidraw+ answers all of these the same | Check the identifier. Then check that the key belongs to the workspace of that collection and that its routes cover it. |
| `invalid-request`: ... is outside the targets of this connection | The collection or scene is outside the connection's targets | Not a key problem. Pass a target the connection allows, or add it; see [Scope](README.md#scope). |
| `invalid-request`: this tool needs a connection with the * target | The tool is workspace-wide | Not a key problem. Use a connection with the `*` target; see [Scope](README.md#scope). |
| `provider-error`: an Excalidraw+ service needs the plain https URL ... | The service address is not the fixed API address | Remove `base_url` or correct it; see [Credential and base URL](README.md#credential-and-base-url). |
| `provider-error`: Excalidraw+ rejected the operation (HTTP ...) | Excalidraw+ refused the request for a reason Qatlas does not classify | Read the provider answer below the message for the reason. |
| `rate-limited`: Excalidraw+ rate-limited the operation | Requests of this key or IP exceeded Excalidraw+'s limit | Wait and retry; see [Rate limit and errors](README.md#rate-limit-and-errors). |
| `unreachable` or `timeout`: Excalidraw+ is unavailable or did not answer in time | Excalidraw+ is in maintenance or slow | Retry later. |
| A change fails with "this change may have taken effect" | The request may have arrived before the failure | Read the scene or user in Excalidraw+, then repeat the change only if it did not apply. Qatlas never repeats a change. |
