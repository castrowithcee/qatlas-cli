---
description: >
  Describes the Infomaniak kChat provider: token setup, the team and channel allow-list, the binding of direct
  and group channels, posts, files, and users, the local file contracts, pagination, the confirmed changes and
  their unclear-result contract, redirect handling, and the boundary to kDrive, Mail, and CalDAV/CardDAV.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-09
---

# Infomaniak kChat

Infomaniak kChat is Infomaniak's Mattermost-compatible team chat product. This provider binds one kChat
instance and one or more of its teams, optionally narrowed to specific channels, and reads and changes
channels and their members, direct and group channels, messages, threads, the threads the token follows,
read state, reactions, pins, attachments, users, the own sidebar categories, channel notifications, and
webhooks through the instance's own `/api/v4/...` REST surface. kChat renders Markdown and mentions such as
`@channel` in a message; Qatlas sends the text as written.

## Configuration

Unlike kDrive, kChat has no shared Infomaniak-wide API root: every team has its own instance host, always
`https://TEAM.kchat.infomaniak.com` (as Infomaniak's own
[mcp-server-kchat](https://github.com/Infomaniak/mcp-server-kchat) builds its requests). `base_url` must be
exactly that shape: `https`, no port, no path, no user, no query, no fragment. Qatlas refuses any other host,
label count, or scheme before a secret is read, lowercases the host, and never follows a redirect from it.

The credential provides `token`, sent as a bearer token: an Infomaniak Manager API token with the kChat scope
(valid account-wide), or a bot token created in the kChat interface, as the `mcp-server-kchat` README
describes; a personal profile token is undocumented and usable only where verified live. A token reaches
every team and channel its owner can reach, possibly of several customers: the connection target, not the
token, decides which teams and channels are reachable.

```yaml
services:
  kchat:
    provider: infomaniakchat
    base_url: https://acme-support.kchat.infomaniak.com

credentials:
  kchat-bot:
    provider: infomaniakchat
    type: keyring
```

## Scope

A connection binds one or more teams and, optionally, an allow-list of their channels:

| Target | Binds |
| --- | --- |
| `team/TEAM_ID` | one kChat team this connection may reach; required, repeatable |
| `channel/CHANNEL_ID` | one channel of a bound team, or one direct or group channel; optional, repeatable |

```yaml
connections:
  support-team:
    service: kchat
    credential: kchat-bot
    targets: [team/abcdEfgh12345678901234ab, channel/chanIdEfgh1234567890abcd]
```

Without a `channel/CHANNEL_ID` target, every channel of the bound teams the token can reach is reachable,
and so is every direct or group channel whose other participants are all members of a bound team. With one
or more, only those channels are, direct and group channels included. A `channel_id`, a reply's `root_id`,
and a `post_id` go through the same two checks `infomaniakdrive` applies to a `drive_id`:

1. A malformed ID, and a `channel_id` outside a configured allow-list, are refused locally, as an invalid
   request, before any secret is read or any request is sent.
2. Every tool with a `channel_id` then confirms live with `GET /api/v4/channels/{channel_id}` that the
   channel belongs to one of the bound teams or, for a direct or group channel, that its participants are
   bound as described below. The allow-list is local configuration a person wrote and is never trusted on
   its own: the same token can belong to teams of several customers. A channel of another team, or a check
   that failed or came back unreadable, aborts the request; there is no silent fallback.

Per tool family:

- `channels.get` takes `channel_id` or `team_id` with `name`; the channel kChat answers with must belong to a
  bound team and be inside the allow-list. `channels.browse` drops channels outside the allow-list.
- `channels.update`, `.archive`, `.restore`, `.privacy`, and the `channelmembers` tools bind their channel
  with the live check and refuse direct and group channels; `channels.update`, `.archive`, and `.privacy`
  also refuse archived channels, and `.restore` accepts only archived ones. `channelmembers.add` proves each
  user a current member of the channel's own team, not merely of some bound team.
- `archivedchannels.list` takes a bound `team_id`, checked locally, and drops channels of other teams,
  outside the allow-list, and direct and group channels.
- The `categories` tools take a bound `team_id`, checked locally, and address the own user. A category
  must belong to that team and user. Shown `channel_ids` are only the reachable channels of the search set;
  any other channel in `channel_ids` is refused before the change, outside the allow-list locally.
  `channelnotifications.update` binds its channel like `channels.update`.
- `channels.create` works only in a bound team and only on a connection without a channel allow-list,
  because a channel that does not exist yet cannot be inside one; the refusal is local.
- A `post_id` (`messages.thread`, `.get`, `.update`, `.delete`, `.pin`, `.unpin`, `.markunread`, `pins`, and
  the `reactions` tools) is bound through its channel: Qatlas reads the post, refuses an answer for another
  post or a deleted post, applies the allow-list to the post's channel, and runs the live check, all before
  any detail is returned or any change is sent. `pins.list` also drops posts kChat reports for another channel.
- A reply confirms, by reading the named `root_id`, that the root post belongs to the same `channel_id`.
- A `thread_id` (`threads.get`, `.follow`, `.unfollow`, `.markread`) is a root `post_id`: it is bound like
  one, must not be a reply, and its channel must be in the reachable set of the bound `team_id` described
  below, so a thread of another bound team is refused.
- A `hook_id` (`incomingwebhooks.*`, `outgoingwebhooks.get`, `.update`, `.delete`) is bound through its
  channel like a post, but only in a bound team. A list keeps webhooks of live channels the token belongs to,
  inside the allow-list. An outgoing webhook without a channel watches every public channel of its team and is
  neither listed nor reachable with a channel allow-list.
- A file (`files.info`, `files.download`) is bound only through the `post_id` kChat reports in its info:
  Qatlas reads the info first, refuses a file without a `post_id` or a deleted file, and applies the post
  binding before any content is requested. `messages.files` binds its `post_id` the same way.
- An upload (`files.upload`) binds its `channel_id` like a message. A file an upload created belongs to no
  message yet, so `messages.send` attaches it only after reading its info: the uploader must be the token's
  own user, no message may hold the file, and, when kChat reports the file's channel, it must be the target
  channel. Any other `file_ids` entry refuses the whole send as an invalid request before the post is sent.

A direct or group channel belongs to no team. It is reachable only when every participant besides the
token's own user is a live member of a bound team, proven with the user check below. A direct channel's
partner comes from kChat's channel name, which must name the own user exactly once; a group channel's
participants come from `GET /api/v4/channels/{channel_id}/members`, which must list the own user and at
most 8 members. Anything else, including an unreadable member list, refuses the channel.

A user is reachable when it is the token's own user (`GET /api/v4/users/me`) or a current member of a bound
team, proven live with `POST /api/v4/teams/{team_id}/members/ids`, one request per bound team and only until
every user is proven; a member who has left the team does not count. `users.get` refuses an unreachable
user, and `users.status` checks all its IDs first, so one foreign ID refuses the whole call. `users.list` and
`.search` drop unreachable users. A `username` is bound through the ID kChat reports before anything is
returned; an unknown username is refused exactly like a foreign one.

The searches (`messages.search`, `files.search`, `channels.search`) and `threads.list` take a bound
`team_id`, checked locally. Qatlas reads the token's channels of that team
(`GET /api/v4/users/me/teams/{team_id}/channels`), keeps only live channels inside the allow-list and never
direct or group channels, sends one request, and drops every hit or thread outside that set, including a
file hit without a `channel_id` and a thread whose root is deleted. Because the set comes from the token's own
channels, a public channel the token has not joined yields no hit.

A successful `qatlas connection test` proves only that the token is accepted, not that a team or channel
of the allow-list exists or is reachable.

## Tools

Every tool ID starts with `infomaniakchat.`; the tool group is the first column:

| Group | Reads | Confirmed changes | Only with a tools list |
| --- | --- | --- | --- |
| `teams` | `teams.list`, `.get`, `teammembers.list` | | `teammembers.roles` |
| `channels` | `channels.list`, `.get`, `.browse`, `.search`, `.unread`, `archivedchannels.list`, `channelmembers.list`, `categories.list` | `channels.create`, `.update`, `.markread`, `channelmembers.add`, `categories.create`, `.update`, `channelnotifications.update` | `channels.archive`, `.restore`, `.privacy`, `channelmembers.remove`, `.roles`, `categories.delete` |
| `messages` | `messages.list`, `.thread`, `.get`, `.files`, `.search`, `direct.list`, `pins.list` | `direct.open`, `groupmessages.open`, `messages.send`, `.update`, `.pin`, `.unpin`, `.markunread` | `messages.delete` |
| `files` | `files.info`, `.search`, `.download` (writes a local file) | `files.upload` (reads a local file) | |
| `threads` | `threads.list`, `.get` | `threads.follow`, `.unfollow`, `.markread` | |
| `reactions` | `reactions.list` | `reactions.add` | `reactions.remove` |
| `users` | `users.get`, `.list`, `.search`, `.status` | `status.set`, `customstatus.set`, `.clear`, `profile.update` | |
| `integrations` | | | `incomingwebhooks.*`, `outgoingwebhooks.*` (`list`, `.get`, `.update`, `.delete`) |

The reads need no confirmation; every other tool always does. The tools of the last column are in no profile
and offered only to a connection whose tools list names them; the role tools alter rights, and
`channels.archive` is kChat's channel delete, which keeps the channel restorable. kChat's own refusals, for
example of the default channel or without team management rights, stay `permission` or `provider-error`.

The terminal editor starts a new connection on the setup profile `read` (all read tools). `messaging` adds
the opens, `messages.send`, `.update`, `.pin`, `.unpin`, `.markunread`, `channels.markread`, `files.upload`,
`reactions.add`, `threads.follow`, `.unfollow`, `.markread`, `categories.create`, `.update`,
`channelnotifications.update`, and the own status tools; `channel-admin` adds `channels.create`, `.update`,
and `channelmembers.add`. `profile.update` and the webhook tools are in no profile.

Behaviour beyond the schemas:

- `teams.get` and `teammembers.*` take a bound `team_id`, checked locally. `teams.get` never returns the
  invitation ID, email, or allowed domains; `teammembers.roles` changes only a live member of that team.
- `channels.create` and `.update` never create or change a direct or group channel; `update` sends only the
  given fields. Re-adding a member or reaction changes nothing; kChat notifies added users.
- Opening a direct or group channel returns the existing one. Every named user is proven a member of a bound
  team first, the own user is added by Qatlas, and repeated IDs count once. A connection with a channel
  allow-list opens no channel, refused before any secret is read; name the channel as a target instead.
- `status.set`, `customstatus.set`, `.clear`, `profile.update`, reactions, categories, notifications, and
  thread following always address the token's own user, read from `GET /api/v4/users/me` or `users/me` in
  the path and never from an argument; the status and profile act for it in every team of the instance (for
  the bot with a bot token). `profile.update` sends only `nickname`, `first_name`, `last_name`, and
  `position`.
- `categories.update` changes only the `display_name` of a custom category and its reachable `channel_ids`;
  other properties are written back as read, and channels the connection cannot reach stay in the category,
  after the given ones. System categories accept only `channel_ids`; `categories.create` and `.delete` handle
  only `custom` ones. Category order is not offered.
- `channelnotifications.update` sends only the given fields of `desktop`, `push`, `email`, and `mark_unread`;
  no direct or group channel.
- `messages.pin`, `.unpin`, `threads.follow`, and `.unfollow` are idempotent, send no body, change nothing
  else, and report the state kChat confirmed. Threads never include deleted threads or participants' user
  data.
- Read state is always the token's own, never an argument, and the marks are idempotent. `channels.unread`
  returns only the counts of the bound channel. `channels.markread` is kChat's channel view, so it also
  clears push notifications for the channel; its body holds `channel_id` only. `threads.markread` takes an
  optional RFC 3339 `timestamp` (now when omitted; refused locally when in the future or before 1970).
- `messages.delete` soft-deletes the post, which users cannot restore; deleting a thread root also removes
  its replies. Whether a token may edit or delete another author's message is kChat's decision (`permission`).
- `files.upload` and `files.download` follow the local file contracts of `infomaniakdrive.files.upload` and
  `.download` (released directories, fixed size limits, no partial file, content never returned). The
  upload's multipart body holds only `channel_id` and the file; kChat's own `413` is reported as too large,
  and kChat cannot list or remove an upload no message holds, so an unattached upload stays. A download
  redirect, for example to a storage host, is a `provider-error` and is not followed. Attachment results
  carry the data sensitivity `infomaniak-kchat-files`, and `include_deleted` is never sent.
- The webhook tools, reads included, need a tools list because an incoming webhook's ID is the secret of its
  post URL; they carry the data sensitivity `infomaniak-kchat-integration-secrets`, `hook_id` is redacted
  from every error, and `.update` writes the webhook back as read. An outgoing webhook's token is never
  decoded or sent, and a callback URL appears only as scheme and host.
- A search term may use kChat's `from:`, `in:`, and `ext:` syntax; the reachable-channel filter applies
  afterwards. The term is never part of an error.
- Users are output through a fixed field allow-list; `email` only when kChat reports one to the token. Roles,
  notification settings, properties, authentication service, and credential or MFA fields are never decoded.
  `users.list` and `.search` need exactly one of `team_id` or `channel_id`; an instance-wide list is not
  offered. `users.search` is a `POST` that changes nothing. User results carry the data sensitivity
  `infomaniak-kchat-people`.

## Pagination

Every list is bounded and paginated, and Qatlas never follows a further page on its own:

- `teams.list`, `channels.list`, `reactions.list`, and `pins.list` read kChat's complete array, which has no
  pagination of its own, and page it themselves: `page` (1-based) and `limit` select a window of the already
  scope-filtered result, and the answer reports `page`, `pages`, `total`, and `count`.
- `channels.browse`, `archivedchannels.list`, the webhook lists, `channelmembers.list`, `teammembers.list`,
  and `users.list` page kChat's own `page` and `per_page`; `has_more` is true when kChat's page was full.
  Dropped channels or users can make `count` lower than the limit. `users.search` takes only `limit` and has
  no further page.
- `messages.list` pages kChat's `GetPostsForChannel`, newest first; `has_more` is kChat's `has_next`.
- `messages.search`, `files.search`, and `threads.list` pass `page` and `limit` (at most 100) to kChat's own
  paging and read no further page; `has_more` is true when kChat's page was full, and `count` may be lower
  because hits outside the reachable channels are dropped. kChat documents the paging of its search as working
  only with Elasticsearch. `channels.search` has no paging. Searches never include archived channels.
- `direct.list` reads kChat's complete channel array of the team, keeps the direct and group channels inside
  the allow-list, and proves only the requested window: one batched user check and one member read per
  group channel, which is why `limit` is at most 50. A failing channel is left out, so `count` may be lower
  than `limit`; `has_more` reports a further window. A channel kChat lists under no team stays reachable
  by its `channel_id`.
- `messages.thread` pages kChat's `GetPostThread` pagination with an opaque `cursor`, kChat's `next_post_id`
  passed back unchanged; the answer reports `has_more` and, while it is true, the next `cursor`.

## Confirmation and unclear results

All confirmed tools require confirmation in their own request and change kChat with exactly one request,
sent after the binding reads, never repeated automatically. An answer that does not match what was
addressed, such as another post or thread or a missing `OK` status, is an unreadable answer. A failure whose
request may nonetheless have reached kChat, such as a timeout, a reset, or a 5xx response, says so in its
message; only the caller decides whether to repeat it.

## Errors

Errors keep stable classes and never carry the token, a message's text, or a raw provider response body:

| Class | Cause |
| --- | --- |
| `auth` | kChat rejected the token |
| `permission` | this token may not perform the operation; check its rights on the team or channel in kChat |
| `not-found` | kChat does not hold the resource, or does not show it to this token |
| `rate-limited` | kChat rate-limited the request; Qatlas applies no proactive spacing and only honours `Retry-After` |
| `timeout` | kChat did not answer in time |
| `unreachable` | kChat could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, or malformed |
| `provider-error` | every other rejection, including a redirect on an endpoint that must not answer with one |

Every refusal described under [Scope](#scope), and every argument outside its fixed set, is an invalid
request, never a provider error, so it is never mistaken for a missing channel or message. It names no
refused target or hook ID and carries no message text into an error or a log.

## Untrusted data

Team, channel, message, category, user, and webhook content are untrusted data: Qatlas normalises it and
never renders it, follows a link in it (a webhook's picture URL included), or executes anything derived
from it, including a message's text.

## Boundary

This provider reaches kChat alone. Infomaniak kDrive, Mail, and CalDAV/CardDAV are separate Infomaniak
products with their own authentication, none of them the token this provider uses; see the `infomaniakdrive`
provider's documentation for why each is its own sibling provider. Within kChat, this provider offers no team
creation, change, or deletion, no adding or removing of team members, no invitation, no permanent deletion,
move, scheme, or moderation change of a channel, no membership change of a direct or group channel, no change
of another user or of other profile fields, no profile picture, no preview or thumbnail, no custom emoji
catalog, no removal of another user's reaction, no read state of another user, no creating of webhooks,
and no change of a webhook beyond its name and description. An edit changes only a message's text, never its
attachments, pin state, or properties.

## Live test scenario

A live test against a real kChat instance checks, in order:

- Team and channel listings match the allow-lists and what the token belongs to; searches show no hit from
  a channel outside the allow-list, a direct message, or another team.
- Channel create and update work only without a channel allow-list; channel and team member tools work on
  connections that list them and refuse a user only in another team.
- A disposable channel switches to private, is archived, shows in `archivedchannels.list`, and is restored
  from a connection that lists the tools; the default channel's refusal is a provider error class.
- Message list ends with `has_more` false on the last of two pages; one confirmed send appears exactly once;
  an upload is sent by `file_id` without `text`; an attached file, another user's upload, and an upload for
  another channel are refused before posting; a download matches `size`; a storage redirect is refused.
- A reply threads under its root; pin and unpin show in `pins.list` without other channels' messages; an
  edit changes only text and edit time; deleting the reply keeps the root; reactions add, list, remove.
- Follow, unfollow, and read marks change only the token's own state, visible in `threads.list`, `.get`, and
  the unread counts; a thread of another team, a reply, and a future timestamp are refused.
- Webhooks list without those of other teams or outside the allow-list (channelless outgoing ones
  included); a disposable one changes name and description only, then is deleted.
- Status, custom status, and profile changes affect only the token's own user and keep other fields; a user
  only in another team is refused by `users.get` and `users.status` and absent from list and search.
- A custom category is created, filled, renamed, and deleted; channels outside the allow-list stay in it;
  `favorites` refuses a rename; notification changes touch only the given fields.
- Direct and group channels open and take messages; `direct.list` shows whether kChat lists them under the
  team at all; a direct channel with a user only in another team is refused.
- Operations outside the targets, or in a team the token cannot reach, are refused before anything changes;
  which token sources an instance accepts, including a personal profile token.
