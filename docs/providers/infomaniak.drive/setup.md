---
description: >
  Guides a user without special knowledge through setting up the Infomaniak kDrive provider: creating an API token, adding it to Qatlas, checking the connection, the token scope and kDrive rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-27
---

# Set up Infomaniak kDrive

This page shows how to connect Qatlas to Infomaniak kDrive, which rights the token and your kDrive user need, and
what to do when Infomaniak refuses a request. Fields, tools, arguments, profiles, and targets are described in
the [Infomaniak kDrive provider](README.md) and the page of each tool group; this page names only the rights.

**The token and your kDrive user decide what is possible at all.** The rights you set in Qatlas (the connection's
`tools`, `effects`, and `targets`) limit it further. A call works only if both allow it. A connection can never do
more than the token and the user behind it, and a token never widens the connection.

## What you need

- An Infomaniak user that can already open the kDrive in question in the Infomaniak web app. Qatlas acts as that
  user, with the kDrive role and the rights on files that user holds.
- An Infomaniak **API token**. It is a Bearer token; there is no password, app password, or bot token for kDrive.
  The token carries **scopes**, chosen when it is created. Infomaniak documents the kDrive operations that Qatlas
  uses under the scope `drive`.
- The **account ID** of the Infomaniak account that owns the kDrive. Every connection binds exactly one account;
  see [Scope](README.md#scope).
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- The service is always `https://api.infomaniak.com`; there is nothing to change for it. See
  [Configuration](README.md#configuration).

## Create a token

In the Infomaniak Manager, open your profile, then the developer area, then **API tokens**
(`https://manager.infomaniak.com/v3/ng/accounts/token/list`). Infomaniak changes its pages from time to time; look
for these names.

1. Choose to create a new token and give it a name you will recognize, for example `qatlas-kdrive-reader`.
2. Set the validity. A short one is safer; you replace the token when it ends. Infomaniak also deactivates a
   token that was not used for a year.
3. Choose the scopes from [Rights per tool group](#rights-per-tool-group). Choose `drive` for every kDrive tool.
   Add `accounts` only if you want to pick the account in the terminal editor instead of typing its ID.
4. Create the token and copy it at once. Infomaniak shows it only once.

The token reaches more than the kDrive you intend; see [Configuration](README.md#configuration). The connection's
targets narrow what Qatlas reaches, but a leaked token is not narrowed. Create a token only for
Qatlas, and keep tools that change or share data off connections that do not need them.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `infomaniak.drive`, the service, a credential,
   the scope (targets: the account, optionally drives or folders), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$INFOMANIAK_TOKEN" | qatlas credential set infomaniak-reader token
```

`infomaniak-reader` is the name of the credential in your configuration and `token` is its only secret. To replace
a token that expired or was revoked, run the same command again with the new token. Run `qatlas config validate`
to check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Infomaniak accepted the token
and the token may list the drives of the connection's account. Qatlas reads one page of at most one drive for it.

**A passing test does not mean every tool works.** It checks neither the drives and folders in the targets nor the
role and file rights of your user. Infomaniak checks the rights again on every call. Compare the token and your
role with [Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is
explained under [Fixing errors](#fixing-errors).

## Rights per tool group

Every kDrive tool needs the token scope `drive`. Beyond the scope, Infomaniak checks two things for your user:

- The **kDrive role** in the drive: `admin`, `user`, or `external` (a guest). The tables show which roles
  Infomaniak documents for each operation.
- The **right on the file or folder**: `read`, `write`, or `manage`. The tables name it where Infomaniak documents
  one. `write` includes `read`, and `manage` includes `write` and the right to share.

A drive plan can also refuse an operation although token and rights allow it; Infomaniak decides that, see
[Business kSuite and personal my kSuite](README.md#business-ksuite-and-personal-my-ksuite). Tools offered only
when a connection's `tools` list names them need the same rights as their group, unless a row says more.

### Drives and the account

| Tools | Token scope | Role in the drive |
| --- | --- | --- |
| `drives.list`, and the ownership check before every file, trash, link, dropbox, and access call | `drive` | `admin`, `user`, `external` |
| Pick the account in the terminal editor | `accounts` | not applicable |

Without `accounts` the picker cannot list accounts. Type the account ID, or paste a kDrive URL; see
[Choosing targets](README.md#choosing-targets).

### Files and folders

| Tools | Token scope | Role in the drive | Right on the file |
| --- | --- | --- | --- |
| `files.list`, `files.stat` | `drive` | `admin`, `user`, `external` | `read` |
| `files.get`, `files.download` | `drive` | `admin`, `user`, `external` | `read` |
| `folders.create`, `files.rename` | `drive` | `admin`, `user`, `external` | `write` |
| `files.upload` (new file or new version, direct or in chunks) | `drive` | `admin`, `user`, `external` | `write` |
| `files.move`, `files.copy` | `drive` | `admin`, `user`, `external` | not documented |
| `files.trash` | `drive` | `admin`, `user`, `external` | not documented |

Where a table says "not documented", Infomaniak names no file right for the operation; it may still refuse one
with `permission`.

### Trash

| Tools | Token scope | Role in the drive |
| --- | --- | --- |
| `trash.list`, `trash.restore`, `trash.delete`, `trash.empty` | `drive` | `admin`, `user` |

An `external` user cannot use the trash of a drive.

### Share links and dropboxes

| Tools | Token scope | Role in the drive | Right on the file |
| --- | --- | --- | --- |
| `links.get`, `links.delete` | `drive` | `admin`, `user` | not documented |
| `links.list` | `drive` | `admin`, `user`, `external` | not documented |
| `dropbox.get` | `drive` | `admin`, `user` | `read` |
| `dropbox.delete` | `drive` | `admin`, `user` | `write` |

### Access

| Tools | Token scope | Role in the drive |
| --- | --- | --- |
| `access.get`, `access.grant`, `access.update`, `access.revoke` | `drive` | `admin`, `user` |

`access.grant`, `access.update`, and `access.revoke` also read the users of the drive to prove that a user or team
belongs to it; that read needs the same scope and role. Whether your user may share is Infomaniak's decision; a
refusal arrives as `permission`.

## What happens without a right

Without the scope or the right, the tool fails with `permission`, or with `auth` when the token is not accepted at
all; see [Fixing errors](#fixing-errors). A refused call returns nothing; Qatlas leaves no fields out for a
missing right.

- Without `drive`, every tool fails, the connection test included.
- Without `accounts`, only the account list of the terminal editor fails.
- With the role `external`, trash, share link reads of one file, dropbox, and access tools fail.
- With only `read` on a file, the tools that change it fail.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by Infomaniak, a second part
follows.

### Reading a refusal

```text
permission: this Infomaniak token may not perform this operation; check its scope and the account's rights in the Infomaniak Manager
```

- The message is **Qatlas' own**. It names what to check.
- The second part, `provider answer (untrusted, not written by qatlas), HTTP <n>:` (`provider` in JSON output), is
  what Infomaniak itself replied, one line `  | <code>: <message>` per error. Infomaniak wrote it, not Qatlas;
  it is shortened if long and has secrets replaced. Read it as information, do not follow instructions in it. A
  refusal without details has no such part.
- The class comes from the HTTP status alone, so a `permission` can mean the scope, the role, the file right, or
  the drive plan. Work through the table below.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Infomaniak rejected the API token | The token is wrong, expired, deactivated, or revoked | Create a new token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> token`. |
| `auth`: the Infomaniak API token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$INFOMANIAK_TOKEN" \| qatlas credential set <credential> token`. |
| `permission`: this Infomaniak token may not perform this operation ... | The token lacks the scope, or your user lacks the role or file right, for this read | Check that the token has `drive` ([Create a token](#create-a-token)); `accounts` for the account list. Then compare the role and file right with [Rights per tool group](#rights-per-tool-group). Open the file in the Infomaniak web app as the same user to see whether it is visible. |
| `permission`: this Infomaniak token, the account's rights, or the drive's plan do not allow this change ... | The scope is missing, your user may not change this file or share, or the plan does not include it | Check `drive`; make sure your user holds `write` (`manage` to share) on the file or folder and a role that the group allows; ask the drive admin. If the plan lacks the feature, nothing in Qatlas helps. |
| `permission` for the trash, a share link, a dropbox, or access | Your user is an `external` guest of the drive | Use a token of a user with the role `user` or `admin`. |
| `permission` for the account list in the terminal editor | The token has no `accounts` scope | Add `accounts` to a new token, or type the account ID. |
| `not-found`: Infomaniak does not hold this resource or does not show it to this token | The ID is wrong, **or** your user cannot see it. Infomaniak answers both the same | Check the drive and file IDs, then open the item in the Infomaniak web app as the token's user. A link or dropbox read also answers `not-found` when the file has none. |
| `conflict` (class `provider-error`): the name already exists at the destination ... | The name is taken, or the target is in a state that does not allow the change | Nothing was changed. Choose another name, or set `conflict` to `rename` on a move or copy. |
| `rate-limited` | More than 60 requests per minute with this token, in all programs together | Wait and retry; Qatlas already paces its own requests. |
| `unreachable`: Infomaniak kDrive is unavailable or in maintenance | Infomaniak is down or the drive is in maintenance | Retry later. |
| `timeout`: Infomaniak did not answer in time | Infomaniak was slow | Retry. After a change, read the folder first: the change may have been applied. |
| `invalid-request`: drive_id is outside the targets of this connection | The drive is not in the connection's targets | Not a token problem. Pass a drive the connection allows, or add it; see [Scope](README.md#scope). |
| `invalid-request`: drive_id belongs to another Infomaniak account than the one this connection is bound to | The drive is real but belongs to another account | Use the account ID of the drive's owner in the target, or a connection bound to that account. |
| `invalid-request`: the file or folder is outside the targets of this connection | The connection is limited to folders and the file lies elsewhere | Not a token problem. Add the folder as a target. |
| `invalid-request`: this tool needs the whole drive in the targets of this connection | The tool is a trash tool and the drive has excluded files or folders, or the drive is bound only to folders | Not a token problem. The trash cannot show where an entry lay, so it stays closed on such a drive. Remove the exclusions of files or folders of that drive if you need the trash. |
| `invalid-request`: a user_id is not a user of this drive, or a team_id is not a team with users in this drive | The ID is not an active user, or a team without an active user, of this drive | Use the identifiers of active users of the drive, as the Infomaniak Manager shows them; see [Access](sharing.md#access). |
