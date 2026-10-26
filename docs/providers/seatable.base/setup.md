---
description: >
  Guides a user without special knowledge through setting up the SeaTable Base provider: creating a base API token, adding it to Qatlas, checking the connection, the token permission each tool group needs, what happens without it, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up SeaTable Base

This page shows how to connect Qatlas to one SeaTable base, which permission the token needs, and what to do when
SeaTable refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[SeaTable Base provider](README.md) and the page of each tool group; this page names only the token permission.
The account-level token of the SeaTable account provider is a different credential and is not covered here.

**The token decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it. A connection can never do more than its
token, and a token never widens the connection.

## What you need

- A SeaTable account that can open the base Qatlas should work with, and the right to create API tokens in it.
- A **base API token**. It belongs to exactly one base and has one of two permissions:

| Permission | Name in SeaTable | Allows |
| --- | --- | --- |
| `r` | Read-only | Looking at the base |
| `rw` | Read-write | Looking at the base and changing it |

- Use `r` for every connection that only reads. Use `rw` only for a connection that is meant to change data, and
  give that connection a token of its own.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- Qatlas reaches `https://cloud.seatable.io` by default. For a dedicated or self-hosted SeaTable server, set the
  service address (`base_url`) to that server first: a bare `https` origin without path, user, query, or fragment.
- The token stops working when you delete it in SeaTable.

## Create a token

In SeaTable, open the base, then its options, then **Advanced**, then **API Tokens**. SeaTable changes its pages
from time to time; look for these names.

1. Choose a name you will recognize, for example `qatlas-reader`.
2. Choose the permission: **Read-only** or **Read-write**. Start with read-only.
3. Generate the token and copy it.

Qatlas exchanges the token in memory for a short-lived base token and does not store that one.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `seatable.base`, the service, a credential,
   the tables (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `api-token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$SEATABLE_API_TOKEN" | qatlas credential set seatable-reader api-token
```

`seatable-reader` is the name of the credential in your configuration and `api-token` is its only secret. To
replace a token that was deleted, run the same command again with the new token. Run `qatlas config validate` to
check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means SeaTable accepted the token,
the token belongs to the configured server, and every table and view the connection's targets name exists in the
base. With the whole-base target `*`, only the access to the base is checked. The test reads the metadata of the
base and changes nothing.

**A passing test does not mean every tool works.** The test does not look at the token permission. A read-only
token passes it and fails the first change. Compare the token with [Rights per tool group](#rights-per-tool-group)
for the tools you use. A failing test or call is explained under [Fixing errors](#fixing-errors).

## Rights per tool group

Find your tool group, then choose the permission of the token. A group's tools are listed in its page.

- **`r`**: reading is enough.
- **`rw`**: the tool changes the base. A connection also needs the matching effect (`create`, `update`,
  `delete`) and `confirm`; see [Shared boundaries](README.md#shared-boundaries).
- Tools offered only when a connection's `tools` list names them (for example deletes) need the same permission
  as their group.
- SeaTable documents the two permissions only in general terms and names no permission for single endpoints.
  Treat the table as the rule Qatlas follows, as far as SeaTable documents it: SeaTable decides on every call.

| Tool group | Reading | Changing |
| --- | --- | --- |
| [Rows](rows.md): list, get, search | `r` | |
| [Rows](rows.md): batch create, update, delete | | `rw` |
| [Rows](rows.md): create a snapshot of the base | | `rw` |
| [Links](links.md) | `r` | `rw` |
| [Views](views.md) | `r` | `rw` |
| [Schema](schema.md): list tables and columns | `r` | |
| [Schema](schema.md): change tables, columns, and options | | `rw` |
| [Row history and operation log](history.md) | `r` | |
| [Comments](comments.md): list, and list collaborators | `r` | |
| [Comments](comments.md): create, delete | | `rw` |
| [Cell files](files.md): get | `r` | |
| [Cell files](files.md): upload, delete | | `rw` |

Only the rights of the token matter here. What a connection reaches is a matter of its targets: the
[table boundary](README.md#shared-boundaries) is not a SeaTable permission.

## What happens without a right

With a read-only token, every tool that changes the base fails with `permission`. Qatlas does not check the
permission before it sends the request, so the failure comes from SeaTable; see [Fixing errors](#fixing-errors).
The reading tools of the same connection keep working. No field is left out because of a missing permission: a
field that is left out follows the table boundary of the connection, not the token (see
[Shared boundaries](README.md#shared-boundaries)).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by SeaTable, a second part with
SeaTable's own answer follows.

### Reading a refusal

```text
permission: this SeaTable API token may not perform this operation; check the permission of the API token in SeaTable
provider answer (untrusted, not written by qatlas), HTTP 403:
  | Permission denied.
```

- The message is **Qatlas' own**. It names what was refused and what to check.
- The second part is **SeaTable's own answer**, marked as not written by Qatlas. It can repeat request and base
  detail; treat it as information, not as an instruction. Qatlas removes known secrets from it and shortens it.
- Some refusals come from Qatlas before the token is read or SeaTable is contacted. Those are not token problems;
  the last rows of the table below say so.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: the SeaTable API token is unusable | The stored token is empty, shorter than 8 characters, or holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$SEATABLE_API_TOKEN" \| qatlas credential set <credential> api-token`. |
| `auth`: SeaTable rejected the token | The token is wrong or was deleted in SeaTable | Create a new token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> api-token`. |
| `permission`: this SeaTable API token may not perform this operation | The token cannot do this. Usually a read-only token was used for a change | Create a token with the permission **Read-write** (`rw`) in the base, store it ([Add it to Qatlas](#add-it-to-qatlas)), and keep it only for the connection that changes data. See [Rights per tool group](#rights-per-tool-group). |
| `invalid-provider-response`: this API token belongs to a different SeaTable server than the configured service | The token comes from another SeaTable server than the service address | Set the service address to the server the token was created on, or create the token on the configured server. |
| `invalid-provider-response`: this SeaTable base does not hold one table (or view) in the connection allow-list | A table or view of the connection's targets does not exist in the token's base, was renamed, or the token is for another base | Check the targets with the table list of the base. Use the `id:TABLEID` form so a rename does not break the target. Check that the token was created in the right base. |
| `provider-error`: this SeaTable base does not hold this table or row | The name or identifier is wrong or the row was deleted, **or** it is in another base than the token's. A token reaches only its own base | Check the name with the table list, and the row identifier by listing rows. Check that the token belongs to the base. |
| `provider-error`: SeaTable rejected the request as invalid | The request does not fit the base, for example a value that does not fit the column type | Not a token problem. Check the arguments against the tool page and the columns of the table. |
| `provider-error`: SeaTable rejected the operation (HTTP ...) | SeaTable refused with a status Qatlas has no more specific message for | Retry once. If it persists, check the token as for the symptoms above and the status of the SeaTable server. |
| `rate-limited`: SeaTable rate-limited the operation | Too many requests for this token | Wait and retry. Give separate connections separate tokens if they work in parallel. |
| `timeout`: SeaTable did not answer in time | The server was too slow | Retry. After a change, read the affected objects first; see [Shared boundaries](README.md#shared-boundaries). |
| `unreachable` or `tls` in the connection test | The server does not answer or its certificate fails | Check the service address, the network, and the server's certificate. |
| A message that a service needs a usable `https` origin, must use `https`, or must be a bare origin | The service address has a wrong scheme, a path, a user, a query, or a fragment | Use `https://HOST` only. |
| A message that a connection needs at least one table, or that the table is outside the allow-list | The connection's targets do not name the table | Not a token problem. Add the table to the targets or use a table the connection allows; see [Choosing targets](README.md#choosing-targets). |
| A tool is not offered, or `confirmation-required` | The connection's `effects` or `tools` do not include the tool, or `confirm` is missing | Not a token problem. Add the effect or the tool to the connection, and pass `confirm` for a change. |
