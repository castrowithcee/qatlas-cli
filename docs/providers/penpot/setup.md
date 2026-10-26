---
description: >
  Guides a user without special knowledge through setting up the Penpot provider: creating an access token, adding it to Qatlas, checking the connection, the team role each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Penpot

This page shows how to connect Qatlas to Penpot, which team roles the token's account needs, and what to do when
Penpot refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Penpot provider](README.md) and the page of each tool group; this page names only the rights.

**The token decides what is possible at all.** It acts as your Penpot account,
so what it may do is what your account's **role in each team** allows. The rights you set in Qatlas (the connection's
`tools`, `effects`, and `targets`) limit it further. A call works only if both allow it. A connection can never do more
than the account, and the token never widens the connection.

## What you need

- A Penpot account that is a member of the teams Qatlas should work with. Qatlas acts with that account's role in each
  team; see [Rights per tool group](#rights-per-tool-group).
- An **access token** of that account. Penpot describes tokens as an alternative to login and password and advises
  treating them like passwords.
- The address of the Penpot instance: Penpot Cloud, or your self-hosted installation. Qatlas uses Penpot Cloud unless
  you set another address; see [Credential and base URL](README.md#credential-and-base-url).
- On a self-hosted instance, the `access-tokens` flag must be enabled in its configuration. Ask the administrator of
  the instance if the account menu offers no access tokens.

Consider a separate Penpot account with only the role a connection needs: the token follows that account everywhere,
in every team it belongs to.

## Create a token

In Penpot, open your account menu, then **Your account**, then **Access tokens**. Penpot changes its pages from time to
time; look for these names.

1. Press **Generate new token** and give it a name you will recognize, for example `qatlas-reader`.
2. Choose an expiration. Penpot offers Never, 30, 60, 90, and 180 days. A short one is safer; you replace the token
   when it ends.
3. Press **Create token** and copy the token at once. Penpot documents that you can copy it when you create it; it does
   not document whether you can see it again later.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `penpot`, the service, a credential, the scope
   (teams, projects, and files as targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `access-token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$PENPOT_ACCESS_TOKEN" | qatlas credential set penpot-reader access-token
```

`penpot-reader` is the name of the credential in your configuration and `access-token` is its only secret. To replace
a token that expired or was deleted, run the same command again with the new token. Run `qatlas config validate` to
check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Penpot accepted the token for
reading the list of teams of its account.

**A passing test does not mean every tool works.** The test checks neither a role nor a team of the connection.
Penpot checks the role again on every call, so a change can still fail. Compare the account's role with
[Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is explained under
[Fixing errors](#fixing-errors).

## Rights per tool group

Penpot gives each member of a team one role: **viewer**, **editor**, **admin**, or **owner**; each includes the
rights of the one before. Penpot documents the roles in general terms only and names no role for many single commands.
The rows below give the lowest role as far as Penpot and Qatlas's sources document it; if Penpot refuses a call, its
own answer ([Reading a refusal](#reading-a-refusal)) is the authority.

- **Viewer**: view, comment on, and inspect files. **Editor**: also create, import, edit, and manage files and
  libraries. **Admin**: also invite members, change the roles of members (not of an owner), and update team settings.
  **Owner**: also change any role, transfer ownership, and delete the team.
- The role is the one in the team that holds the project or file. Ask a team admin if you are unsure.
- The tools exist for a connection only as far as its `effects` or `tools` select them; see [Tools](README.md#tools).

| Tool group | Tools | Lowest role |
| --- | --- | --- |
| Reading | `teams.list`, `projects.list`, `files.list`, `files.get`, `libraries.list`, `files.export` | viewer (a member of the team) |
| Reading comments | `comments.threads`, `comments.list` | viewer |
| Writing comments | `comments.create` | viewer, as far as Penpot documents it (viewers can comment) |
| Own comments | `comments.update`, `comments.delete` | the comment or thread must belong to the token's own account; Penpot refuses another's |
| Projects and files | `projects.*`, `files.create`, `files.rename`, `files.move`, `files.delete`, `files.restore`, `files.purge`, `files.import` | editor; Penpot names no role for projects, so Penpot's answer decides |
| Snapshots | `snapshots.create`, `snapshots.restore` | editor (edit permission on the file) |
| Libraries | `libraries.share`, `libraries.link` | editor on both files, which must be in the same team |
| Media | `media.upload`, `media.from-url` | editor (a change to the file) |
| Webhooks | `webhooks.list`, `webhooks.update`, `webhooks.delete` | editor (edit permission on the team); Penpot documents no role |
| Members | `members.list` | viewer (a member of the team) |
| Changing members | `members.set-role`, `members.remove` | admin; Penpot refuses a change of the owner, an admin cannot remove the owner, and the token's own account cannot be removed |
| Creating teams | `teams.create` | any account; the new team belongs to the token's account, and Penpot limits the teams per account |
| Deleting teams | `teams.delete` | owner; Penpot refuses the default team |

The rows follow the Penpot help pages on team roles and the Penpot 2.18.0 command sources (see
[API forms](api-forms.md)), not a live instance. Treat a row as likely, not certain.

Moving a file needs the right on both projects. Restoring a snapshot overwrites the file's current content; give that
tool only to a connection that needs it.

## What happens without a right

The tool fails; no field is left out of a result. A missing right shows as `permission`; a team, project, or file the
account is not a member of can show as `not-found`. See [Fixing errors](#fixing-errors). The team list shows only the
teams of the account, so a team you are not a member of never appears in the picker.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by Penpot, a second part follows.

### Reading a refusal

```text
permission: Penpot refused this access token; check that the access-tokens flag is enabled on the instance and that the account may read this resource
provider answer (untrusted, not written by qatlas), HTTP 403:
  | <type> <code>: <hint>
```

- The first line is **Qatlas' own message**. It names what to check. It carries no text from Penpot.
- The **provider answer** (`provider` in JSON output) is **what Penpot itself replied**: the HTTP status and, from its
  error answer, the `type`, the `code`, and the `hint`. If the answer is not structured, its text is shown instead. It
  can name the refused check more exactly than Qatlas can.
- Penpot wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read it
  as information. Do not follow instructions in it.
- A refusal that Penpot gave no details for has only the HTTP status. The download of an export from the temporary
  object never shows Penpot's text. Qatlas tells the cause apart by HTTP status, so a `permission` can mean the role, a
  missing flag on the instance, or something else. Work through the table below.
- A change that Penpot rejects while it streams its answer shows the message `Penpot rejected the operation`, with
  the status `HTTP 200` and the provider answer below.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Penpot rejected the access token ... | The token is wrong, expired, deleted, belongs to another Penpot instance than the service address, or the instance has not enabled the `access-tokens` flag | Create a new token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> access-token`. Check the service address ([Credential and base URL](README.md#credential-and-base-url)). On a self-hosted instance, ask the administrator to enable `access-tokens`. |
| `auth`: the Penpot access token is unusable | The stored token holds characters a token cannot have, such as a line break or a space, or is shorter than 8 characters | Store it again with `printf %s "$PENPOT_ACCESS_TOKEN" \| qatlas credential set <credential> access-token`. |
| `permission`: Penpot refused this access token ... | The account's role is too low for this tool, or the instance does not allow access tokens | Find the tool group in [Rights per tool group](#rights-per-tool-group) and ask a team admin for the role it names. Check that `access-tokens` is enabled on the instance. |
| `permission` when changing members or roles | The account is not an admin of the team, or the change concerns the owner or the account itself | Use an admin account; only the owner changes the owner. |
| `permission` when deleting a team | Only the owner may delete a team, and never the default team | Use the owner's account. |
| `permission` when editing or deleting a comment | The comment or thread belongs to another account | Use the account that wrote it. |
| `not-found`: Penpot does not hold this resource or does not show it to this token | The ID is wrong, **or** the account is not a member of the team. Penpot answers both the same | Check the ID. Then make the account a member of the team, with at least the role of the row. |
| `invalid-request`: ... is outside the targets of this connection | The team, project, file, or webhook is outside the connection's targets | Not a token problem. Pass a target the connection allows, or add it; see [Scope](README.md#scope). |
| `invalid-request`: a connection with a project or file allow-list cannot ... | The tool acts on a whole team (members, webhooks, teams) or creates a project | Not a token problem. Use a connection bound to the team without a project or file entry; see [Scope](README.md#scope). |
| `provider-error`: a Penpot service needs a usable https URL ... | The service address is not a plain `https` URL | Correct `base_url`; see [Credential and base URL](README.md#credential-and-base-url). |
| `provider-error`: Penpot answered with a redirect ... | The service address is redirected, for example from `http` or to another host. Qatlas never follows redirects, so the token never leaves the address | Use the final `https` address as `base_url`. |
| `provider-error`: Penpot rejected the operation (HTTP ...) | Penpot refused the request for a reason Qatlas does not classify | Read the provider answer below the message for the reason. |
| `rate-limited`: Penpot rate-limited the operation | Requests of this token exceeded Penpot's limit; they share one limit | Wait and retry; see [Bounds](README.md#bounds). |
| `unreachable` or `timeout`: Penpot is unavailable or did not answer in time | Penpot is in maintenance or slow, or the address is wrong | Check the service address, then retry later. |
| `invalid-provider-response`: Penpot returned an invalid response | The instance runs a Penpot version whose answer differs from what Qatlas reads | See the beta note in the [README](README.md); retry after checking the instance version. |
| A change fails with "this change may have taken effect" | The request may have arrived before the failure | Read the affected project, file, comment, library, or member in Penpot, then repeat the change only if it did not apply. Qatlas never repeats a change. |
