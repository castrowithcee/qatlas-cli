---
description: >
  Guides a user without special knowledge through setting up the Baserow provider: creating a database token, adding it to Qatlas, checking the connection, the token rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Baserow

This page shows how to connect Qatlas to Baserow, which rights the token needs, and what to do when a request fails.
Fields, tools, arguments, and targets are described in the [Baserow provider](README.md) and its sections; this page
names only the token rights.

**The token decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it. A connection can never do more than its
token, and a token never widens the connection.

## What you need

- A Baserow account that is an admin or builder of the workspace whose tables Qatlas should work with. Baserow
  documents that only workspace admins and builders can create database tokens.
- A **database token**. Baserow has no other credential that Qatlas uses: a login with a user name and password or a
  session token (JWT) is not supported, so everything that Baserow reserves for a user session stays out of reach.
  That includes creating, changing, or deleting tables and fields, row history, and row comments; see
  [Not offered](README.md#not-offered-row-history-and-comments).
- A database token belongs to one workspace and carries the rights **Create**, **Read**, **Update**, and **Delete**,
  set for all tables of the workspace or for chosen tables.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- Qatlas reaches Baserow Cloud (`https://api.baserow.io`) by default. For a self-hosted instance, set the service
  address first; see [Credential and base URL](README.md#credential-and-base-url).

## Create a token

In Baserow, click your workspace in the top-left corner, open **Settings**, then the **Database tokens** tab. Baserow
changes its pages from time to time; look for these names.

1. Click **Create token +** and give it a name you will recognize, for example `qatlas-reader`.
2. Select the workspace whose tables Qatlas should reach.
3. Click **Create token** and copy the token at once. Baserow does not show it again once the dialog closes.
4. Set the rights from [Rights per tool group](#rights-per-tool-group) for the tools you will use, and choose
   all tables of the workspace or only the tables Qatlas needs. Start with **Read**.

Give a token only what a connection needs. A connection that only reads should have a token that only reads, and a
token limited to the tables of the connection. The connection's targets limit only what Qatlas shows; they do not
narrow what the token itself may read.

Baserow lets you rename a token, generate a new value for it (the old value stops working at once), and delete it.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `baserow`, the service, a credential, the scope
   (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `database-token`
   secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$BASEROW_TOKEN" | qatlas credential set baserow-reader database-token
```

`baserow-reader` is the name of the credential in your configuration and `database-token` is its only secret. To
replace a token that was regenerated or deleted, run the same command again with the new token. Run
`qatlas config validate` to check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Baserow accepted the token for
reading its table list.

**A passing test does not mean every tool works.** The test needs only the right to read. Baserow checks the rights
again on every call, so a row change or a file upload can still fail. Compare the token with
[Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is explained under
[Fixing errors](#fixing-errors).

## Rights per tool group

Find your tool group, then enable the right on the token. Qatlas names the right it needs when Baserow refuses a
change. As far as Baserow documents it, **Create** and **Update** include reading.

| Tools | Right on the token |
| --- | --- |
| `baserow.tables.list`, `baserow.fields.list` | Read |
| `baserow.rows.list`, `baserow.rows.get`, `baserow.rows.search` | Read |
| `baserow.rows.create`, `baserow.rows.batch-create` | Create |
| `baserow.rows.update`, `baserow.rows.batch-update` | Update |
| `baserow.rows.move` | Update |
| `baserow.rows.delete`, `baserow.rows.batch-delete` | Delete |
| `baserow.files.upload` | Update (Qatlas asks for it; Baserow does not document which right an upload needs) |
| `baserow.files.get` | Read |

- The tools above exist for a connection only as far as its `effects` or `tools` select them. The `read` profile
  offers the five read tools; the delete tools run only when the connection's `tools` list names them.
- The token's rights apply to the workspace or tables you chose. A table the token does not cover answers
  `permission` or `not-found`.
- Row changes and file uploads also read the table's fields first, so they need the right to read as well.

## What happens without a right

The tool fails; no field is left out of a result. A missing right shows as `permission`, and the message names the
right to grant. A table the token does not cover can instead show as `not-found`. See
[Fixing errors](#fixing-errors).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas.

### Reading a refusal

```text
permission: this database token may not update this resource; check its update right for the table
```

- The message is **Qatlas' own**. It names the right the call needs and what to check.
- No second part follows. Qatlas never passes on Baserow's own text, so a Baserow refusal has no `provider` part.
  Qatlas reads only the HTTP status and chooses the class and message from it.
- A message that ends with "this change may have taken effect, read the row before repeating it" means the request
  may have reached Baserow. Qatlas never repeats a change. Read the row first, then decide.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Baserow rejected the database token | The token is wrong, was regenerated or deleted, or belongs to another Baserow than the service address | Create or copy the token again ([Create a token](#create-a-token)) and store it with `qatlas credential set <credential> database-token`. For a self-hosted instance, check the service address. |
| `auth`: the Baserow database token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$BASEROW_TOKEN" \| qatlas credential set <credential> database-token`. |
| `permission`: this database token may not read this resource | The token lacks the right to read, or does not cover the table | Enable **Read** on the token and include the table; see [Rights per tool group](#rights-per-tool-group). |
| `permission`: this database token may not create / update / delete this resource | The token can read but not make this change | Enable the right named in the message (**Create**, **Update**, or **Delete**) for the table. A move and a file upload need **Update**. |
| `permission`: Baserow refused to serve this file | The instance denied the download of the stored file | Not a token right: the download sends no token. Check how the instance serves its files; see [Files](README.md#files). |
| `not-found`: Baserow does not hold this resource or does not show it to this token | The table, row, or field does not exist, **or** the token does not cover the table. Baserow answers both the same | Check the IDs. Then check that the token covers the table (all tables of the workspace or the chosen ones) and was created in the workspace of that table. |
| `invalid-request`: table_id is outside the targets of this connection | The table is not in the connection's targets | Not a token problem. Pass a table the connection allows, or add it; see [Scope](README.md#scope). |
| `provider-error`: a Baserow service needs a usable https URL | The service address is not `https`, or has user info, a query, or a fragment | Correct `base_url`; see [Credential and base URL](README.md#credential-and-base-url). |
| `rate-limited`: Baserow rate-limited the operation | Requests of this token exceeded Baserow's limit | Wait and retry. Requests of one token share the limit; see [Bounds](README.md#bounds). |
| `unreachable` or `timeout`: Baserow is unavailable or did not answer in time | Baserow is in maintenance, or the instance is slow or not reachable | Retry later; for a self-hosted instance, check that it runs and the service address is right. |
| A change fails with "this change may have taken effect" | The request may have arrived before the failure | Read the row, then repeat the change only if it did not apply; see [Row changes](README.md#row-changes). |
