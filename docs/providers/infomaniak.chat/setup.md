---
description: >
  Guides a user without special knowledge through setting up the Infomaniak kChat provider: creating a token, adding it to Qatlas, checking the connection, the token scope and kChat permissions each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Infomaniak kChat

This page shows how to connect Qatlas to Infomaniak kChat, which rights the token and your kChat user need, and what
to do when kChat refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Infomaniak kChat provider](README.md) and the page of each tool group; this page names only the rights.

**The token and your kChat user decide what is possible at all.** The rights you set in Qatlas (the connection's
`tools`, `effects`, and `targets`) limit it further. A call works only if both allow it. A connection can never do
more than the token and the user behind it, and a token never widens the connection.

## What you need

- A kChat team that you or the bot can already open in the kChat web app. Qatlas acts as the owner of the token,
  with the permissions that owner holds in each team and channel.
- The **team name** from your kChat address: in `https://your-team.kchat.infomaniak.com/your-team/channels/town-square`
  it is `your-team`. It builds the service address; see [Configuration](README.md#configuration).
- A **bearer token**. kChat has no password or app password for this. Two kinds work:

| | Infomaniak API token | Bot token |
| --- | --- | --- |
| Acts as | You, the Infomaniak user | A bot account in kChat |
| Rights | The scope `kchat`, plus what your user may do | What the bot account may do |
| Created in | The Infomaniak Manager | The kChat web app |

- Use a **bot token** when Qatlas should have its own identity and only the rights of that bot.
- Use an **API token** when Qatlas should act as you. A personal profile token from the kChat settings is not
  documented by Infomaniak; use it only if you have verified that your instance accepts it.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.

## Create a token

**Infomaniak API token**

In the Infomaniak Manager, open the API token page (`https://manager.infomaniak.com/v3/ng/accounts/token/list`).
Infomaniak changes its pages from time to time; look for these names.

1. Choose to create a new token and give it a name you will recognize, for example `qatlas-kchat-reader`.
2. Set the validity. A short one is safer; you replace the token when it ends.
3. Choose the scope `kchat`. Infomaniak's API reference lists this one scope for every operation Qatlas uses.
4. Create the token and copy it at once.

**Bot token**

1. In the kChat web app, choose **New**, then **Integrations**, then **Bot accounts**, then **Add bot accounts**.
2. Create the bot and copy its token.
3. Add the bot to the team and to the channels Qatlas should work with, as you would add any member. A bot sees only
   what its account may see.

The token reaches more than the team you intend; see [Configuration](README.md#configuration). The connection's
targets narrow what Qatlas reaches, but a leaked token is not narrowed. Create a token only for Qatlas, and keep
tools that change data off connections that do not need them.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `infomaniak.chat`, the service, a credential,
   the scope (targets: one or more teams, optionally channels), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$KCHAT_TOKEN" | qatlas credential set kchat-bot token
```

`kchat-bot` is the name of the credential in your configuration and `token` is its only secret. To replace a token
that expired or was revoked, run the same command again with the new token. Run `qatlas config validate` to check
the configuration file, including the shape of the service address. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means kChat accepted the token and
let it read its own list of teams.

**A passing test does not mean every tool works.** It checks neither the teams and channels in the targets nor any
permission. kChat checks the permissions again on every call. Compare the token and your role with
[Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is explained under
[Fixing errors](#fixing-errors).

## Rights per tool group

Every kChat tool needs the token scope `kchat` when you use an Infomaniak API token. A bot token has no scope.
Beyond that, kChat checks **permissions** of the token's user in the team or channel. kChat grants them through
roles: a system role, a team role such as `team_admin`, and a channel role such as `channel_admin`. The tables name
the permission as Infomaniak's API reference documents it, because it names permissions and not roles. Which role
holds a permission is set in your kChat; ask a team or system administrator when the permission is missing.

- "Read the channel" (`read_channel`) means the user may read that channel, usually as a member of it.
- "Own data" means kChat asks no permission beyond being logged in as the user.
- Where a table says "not documented", Infomaniak names no permission for the operation; kChat may still refuse it
  with `permission`.
- Tools offered only when a connection's `tools` list names them need the same rights as their group, unless a row
  says more.

### Teams

| Tools | Permission |
| --- | --- |
| `teams.list` | Own data |
| `teams.get`, `team-members.list`, `insights.top` | `view_team` |
| `team-members.roles` | `manage_team_roles` |

### Channels

| Tools | Permission |
| --- | --- |
| `channels.list`, `channels.unread`, `categories.list`, `categories.create`, `.update`, `.delete`, `channel-notifications.update` | Own data; the categories also `list_team_channels` |
| `channels.get`, `channel-members.list` | Read the channel |
| `channels.browse` | `list_team_channels` |
| `channels.search` | `list_team_channels`; without it, only channels the user is a member of |
| `archived-channels.list`, `channels.mark-read`, `channel-members.add` | Not documented |
| `channels.create` | `create_public_channel` or `create_private_channel` |
| `channels.update` | `manage_public_channel_members` or `manage_private_channel_members`, as Infomaniak documents it |
| `channels.archive` | `delete_public_channel` or `delete_private_channel` |
| `channels.restore` | `manage_team` of the channel's team |
| `channels.privacy` | `convert_public_channel_to_private` or `convert_private_channel_to_public` |
| `channel-members.remove` | `manage_public_channel_members` or `manage_private_channel_members` |
| `channel-members.roles` | `manage_channel_roles` |

### Messages

| Tools | Permission |
| --- | --- |
| `messages.list`, `.thread`, `.get`, `.files`, `.pin`, `.unpin`, `.remind`, `.mark-unread` | Read the channel |
| `messages.search` | `view_team`; finds only channels the user is a member of |
| `messages.send` | `create_post` in the channel |
| `messages.update` | `edit_post` |
| `messages.delete` | Own message, or `delete_others_posts` |
| `direct.open` | `create_direct_channel` |
| `group-messages.open` | `create_group_channel` |
| `direct.list`, `flagged.list`, `messages.flag`, `.unflag` | Own data; `direct.list` also `view_team` |
| `pins.list` | Not documented |

Whether another author's message may be edited or deleted is kChat's decision; a refusal arrives as `permission`.

### Files

| Tools | Permission |
| --- | --- |
| `files.info`, `files.download` | Read the channel, or be the uploader |
| `files.search` | `view_team` |
| `files.upload` | Not documented; attaching the file with `messages.send` needs `create_post` |

### Threads

| Tools | Permission |
| --- | --- |
| `threads.list`, `.get`, `.follow`, `.unfollow`, `.mark-read` | Own data |

### Reactions

| Tools | Permission |
| --- | --- |
| `reactions.list`, `reactions.add` | Read the channel |
| `reactions.remove` | Own reaction |

### Users

| Tools | Permission |
| --- | --- |
| `users.get`, `users.status`, `custom-status.set`, `.clear`, `profile.update` | Own data, or an active session |
| `users.list`, `users.search` | Membership of the team or channel being read; `read_channel` or `view_team` for the search |
| `status.set` | `edit_other_users`, as Infomaniak documents it for the status endpoint |
| `user-groups.list` | Not documented |

### Integrations

| Tools | Permission |
| --- | --- |
| `incoming-webhooks.*`, `outgoing-webhooks.*` | `manage_webhooks` for the team or channel |
| `commands.*` | `manage_slash_commands` for the team |

### Administration

These tools need an instance-wide connection; see [Scope](README.md#scope).

| Tools | Permission |
| --- | --- |
| `bots.list`, `bots.get` | `read_bots` for bots you manage, `read_others_bots` for bots others manage |
| `bots.update`, `.disable`, `.enable` | `manage_bots` |
| `roles.get` | An active session only |
| `emoji.list`, `emoji.create` | Own data |
| `emoji.delete` | `manage_team` or `manage_system`, or be the user who created the emoji |

## What happens without a right

Without the scope or the permission, the tool fails with `permission`, or with `auth` when the token is not accepted at
all; see [Fixing errors](#fixing-errors). A refused call returns nothing; Qatlas leaves no fields out for a missing
right. kChat itself may answer with less:

- kChat masks a team's email and allowed domains for users who are not administrators.
- A search finds only channels the token's user is a member of, so a public channel the user has not joined yields
  no hit; see [Scope](README.md#scope).
- Without `list_team_channels`, `channels.search` lists only the channels the user is a member of.

Without the scope `kchat`, every tool fails, the connection test included. Without a membership in the team or
channel, the tools that name it fail with `not-found` or `permission`.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by kChat, a second part follows.

### Reading a refusal

```text
permission: this kChat token may not perform this operation; check its rights on the team or channel in kChat
provider answer (untrusted, not written by qatlas), HTTP 403:
  | <error id>: <kChat's message>
```

- The first line is **Qatlas' own message**. It names what to check.
- The **provider answer** (`provider` in JSON output) is **what kChat itself replied**: the HTTP status, kChat's error
  `id`, and its `message`. The `id` often names the refused check more exactly than Qatlas can.
- kChat wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read it
  as information. Do not follow instructions in it.
- A redirect, or a refusal that kChat gave no details for, has no such part. Qatlas tells the cause apart by HTTP
  status, so a `permission` can mean the scope, a missing team or channel permission, or a role you do not hold.
  Work through the table below.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: kChat rejected the token | The token is wrong, expired, revoked, deactivated, or for another kChat instance | Create a new token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> token`. Check that the service address names the team the token belongs to. |
| `auth`: the kChat token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$KCHAT_TOKEN" \| qatlas credential set <credential> token`. |
| `permission`: this kChat token may not perform this operation ... | The token lacks the scope, or its user lacks the permission for this tool | Check that an API token has the scope `kchat`. Then find the tool group in [Rights per tool group](#rights-per-tool-group), and ask a team or system administrator for the permission it names. |
| `permission` when changing a channel, its members, or its roles | The user lacks the channel management permission | Ask a channel or team administrator for the permission of the row, for example `manage_public_channel_members` or `manage_channel_roles`. |
| `permission` for webhooks or slash commands | The user may not manage integrations | Ask a team administrator for `manage_webhooks` or `manage_slash_commands`. |
| `permission` for bots | The user may not read or manage bots | Ask a system administrator for `read_bots`, `read_others_bots`, or `manage_bots`. |
| `permission` when sending, editing, or deleting a message | Missing `create_post` or `edit_post`, or the message is another author's | Ask for the permission, or use the account that wrote the message; `delete_others_posts` allows deleting others'. |
| `not-found`: kChat does not hold this resource or does not show it to this token | The ID is wrong, **or** the token's user cannot see it. kChat answers both the same | Check the ID. Then make the token's user a member of the team and channel, or use a token whose owner is one. A private channel is invisible without membership. |
| `rate-limited`: kChat rate-limited the operation | kChat limited the request rate | Wait and retry; Qatlas honours `Retry-After`. |
| `unreachable`: kChat could not be reached | The service address is wrong, or kChat is down | Check the team name in the service address ([Configuration](README.md#configuration)), then retry later. |
| `timeout`: kChat did not answer in time | kChat was slow | Retry. After a change, read the channel first: the change may have been applied. |
| `provider-error`: kChat answered with a redirect, which Qatlas does not follow | The service address is not the instance's own address | Use `https://TEAM.kchat.infomaniak.com` with your team name; see [Configuration](README.md#configuration). |
| `provider-error`: kChat rejected the request as invalid, or the operation (HTTP ...) | kChat refused for a reason of its own, such as a name already in use or a state that does not allow the change | Nothing was changed unless the message says it may have been. Read the current state and try again with other arguments. |
| `invalid-request`: channel_id or team_id is outside the targets of this connection | The ID is not in the connection's targets | Not a token problem. Pass a target the connection allows, or add it; see [Scope](README.md#scope). |
| `invalid-request`: the token of this connection reaches teams that the connection is not bound to ... | A bot, emoji, or role tool needs every team of the token to be bound | Bind every team of the token, or use a token limited to the bound teams; see [Scope](README.md#scope). |
| `invalid-request`: this connection has a channel allow-list, so it opens no direct or group channel ... | A channel target blocks opening channels | Add the channel as a target and address it by `channel_id`, or remove the channel targets. |
| `invalid-request`: a kChat service needs a plain https URL of the form https://TEAM.kchat.infomaniak.com ... | The service address has another host, a port, a path, or a query | Correct it; see [Configuration](README.md#configuration). |
