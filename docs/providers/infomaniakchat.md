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
channels, channel members, direct and group channels, messages, threads, reactions, pins, attachments, and
users through the instance's own `/api/v4/...` REST surface. kChat renders Markdown and mentions such as
`@channel` in a message; Qatlas sends the text as written.

## Configuration

Unlike kDrive, kChat has no shared Infomaniak-wide API root: every team has its own instance host, always
`https://TEAM.kchat.infomaniak.com` (confirmed by Infomaniak's own
[mcp-server-kchat](https://github.com/Infomaniak/mcp-server-kchat), which builds its requests from that host).
`base_url` must be exactly that shape: `https`, no port, no path, no user, no query, no fragment. Qatlas
refuses any other host, label count, or scheme before a secret is read, lowercases the host, and never follows
a redirect away from it.

The credential provides `token`, sent as a bearer token: an Infomaniak Manager API token with the kChat scope
(valid account-wide), or a bot token created in the kChat interface, as the `mcp-server-kchat` README
describes. A personal token from the kChat profile is not a documented source; it is usable only where it
has been verified live against an instance. Such a token reaches every team and channel its owner can reach,
which matters for a person who works across several customers' teams: the connection target, not the token,
decides which teams and channels are reachable.

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

Refusals are invalid requests, never name the foreign target, and never carry message text into an error or
a log.

Per tool family:

- `channels.get` takes `channel_id` or `team_id` with `name`; the channel kChat answers with must belong to a
  bound team and be inside the allow-list. `channels.browse` drops channels outside the allow-list.
- `channels.update` and the `channelmembers` tools bind their channel with the live check and refuse direct
  and group channels; `channels.update` also refuses archived channels. `channelmembers.add` proves each
  user a current member of the channel's own team, not merely of some bound team.
- `channels.create` works only in a bound team and only on a connection without a channel allow-list,
  because a channel that does not exist yet cannot be inside one; the refusal is local.
- A `post_id` (`messages.thread`, `.get`, `.update`, `.delete`, `.pin`, `.unpin`, `pins`, and the `reactions`
  tools) is bound through its channel: Qatlas reads the post, refuses an answer for another post or a
  deleted post, applies the allow-list to the post's channel, and runs the live check, all before any
  detail is returned or any change is sent. `pins.list` also drops every post kChat reports for another
  channel.
- A reply confirms, by reading the named `root_id`, that the root post belongs to the same `channel_id`.
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
most 8 members. Anything else, including an unreadable member list, refuses the channel. `users.list` and
`.search` with its `channel_id` still drop every user outside the bound teams.

A user is reachable when it is the token's own user (`GET /api/v4/users/me`) or a current member of a bound
team, proven live with `POST /api/v4/teams/{team_id}/members/ids`, one request per bound team and only until
every user is proven; a member who has left the team does not count. `users.get` refuses an unreachable
user, and `users.status` checks all its IDs first, so one foreign ID refuses the whole call. `users.list` and
`.search` drop unreachable users. A `username` is bound through the ID kChat reports before anything is
returned; an unknown username is refused exactly like a foreign one.

The searches (`messages.search`, `files.search`, `channels.search`) take a bound `team_id`, checked locally.
Qatlas reads the token's channels of that team (`GET /api/v4/users/me/teams/{team_id}/channels`), keeps only
live channels inside the allow-list and never direct or group channels, sends one search request, and drops
every hit outside that set, including a file hit without a `channel_id`. Because the set comes from the
token's own channels, a public channel the token has not joined yields no hit.

A successful `qatlas connection test` proves only that the token is accepted, not that every team or channel
of the allow-list exists, is reachable, or belongs to it.

## Tools

Every tool ID starts with `infomaniakchat.`; the tool group is the first column:

| Group | Reads | Confirmed changes | Only with a tools list |
| --- | --- | --- | --- |
| `teams` | `teams.list` | | |
| `channels` | `channels.list`, `.get`, `.browse`, `.search`, `channelmembers.list` | `channels.create`, `.update`, `channelmembers.add` | `channelmembers.remove`, `.roles` |
| `messages` | `messages.list`, `.thread`, `.get`, `.files`, `.search`, `direct.list`, `pins.list` | `direct.open`, `groupmessages.open`, `messages.send`, `.update`, `.pin`, `.unpin` | `messages.delete` |
| `files` | `files.info`, `.search`, `.download` (writes a local file) | `files.upload` (reads a local file) | |
| `reactions` | `reactions.list` | `reactions.add` | `reactions.remove` |
| `users` | `users.get`, `.list`, `.search`, `.status` | | |

The reads need no confirmation; every other tool always does, through the confirmation mechanism of every
confirmed Qatlas tool. The tools of the last column are in no profile and offered only to a connection whose
tools list names them; `channelmembers.roles` alters rights and accepts exactly `channel_user` and
`channel_user channel_admin`.

The terminal editor starts a new connection on the setup profile `read` (all read tools, `direct.list` and
the searches included). `messaging` adds `direct.open`, `groupmessages.open`, `messages.send`,
`messages.update`, `files.upload`, `reactions.add`, `messages.pin`, and `messages.unpin`. `channel-admin`
reads teams and channels and adds `channels.create`, `channels.update`, and `channelmembers.add`.

Behaviour beyond the schemas:

- `channels.create` and `channels.update` never create or change a direct or group channel; `update` sends
  only the given fields. Adding an existing member or reaction changes nothing, and kChat notifies added
  users.
- Opening a direct or group channel returns the existing one. Every named user is proven a member of a bound
  team first, the own user is added by Qatlas, and repeated IDs count once. A connection with a channel
  allow-list opens no channel, refused before any secret is read, because the channel could fall outside
  the list; name the channel as a target instead.
- `messages.pin` and `.unpin` take only a `post_id`, are idempotent, change nothing else of the message,
  and report the state kChat confirmed.
- `messages.delete` soft-deletes the post, which users cannot restore; deleting a thread root also removes
  its replies. Whether a token may edit or delete another author's message is kChat's decision; a refusal
  is `permission`, as for pinning.
- Reactions are always those of the token's own user, whose ID Qatlas reads from `GET /api/v4/users/me` in
  the same call, never from an argument.
- `files.upload` takes exactly one source: `local_path` inside a directory the connection releases for
  reading (`files: read`), or `content_base64` with a `name`, as in `infomaniakdrive.files.upload`. Qatlas
  builds the multipart body itself with only `channel_id` and the file. A file above the fixed Qatlas
  upload limit is refused before any request; kChat's own `413` is reported as too large. The result
  carries `file_id`, `name`, `size`, and `mime_type`, never content. kChat has no operation to list or
  remove an upload no message holds, so an unattached upload stays in kChat.
- `files.download` follows the local file contract of `infomaniakdrive.files.download`: it writes only
  inside a directory released for writing (`files: write`), replaces an existing file only with
  confirmation, and leaves no file when the transfer is incomplete or its size differs from the size in the
  file's info. A transfer above the fixed Qatlas file limit is refused. The result carries only `file_id`,
  `name`, `size`, and `sha256`; the content is streamed to the file and never decoded or returned. A
  redirect, for example to a storage host, is a `provider-error` and is not followed. Attachment results
  carry the data sensitivity `infomaniak-kchat-files`; file names and media types are untrusted data, and
  `include_deleted` is never sent.
- A search term may use kChat's `from:`, `in:`, and `ext:` syntax; the reachable-channel filter applies
  afterwards. The term is never part of an error.
- Users are output through a fixed field allow-list; `email` only when kChat reports one to the token.
  Roles, notification settings, properties, authentication service, and credential or MFA fields are never
  decoded. `users.list` and `.search` need exactly one of `team_id` or `channel_id`; an instance-wide list
  is not offered. `users.search` is a `POST` that changes nothing. User results carry the data sensitivity
  `infomaniak-kchat-people`.

## Pagination

Every list is bounded and paginated, and Qatlas never follows a further page on its own:

- `teams.list`, `channels.list`, `reactions.list`, and `pins.list` read kChat's complete array, which has
  no pagination of its own, and page it themselves: `page` (1-based) and `limit` select a window of the
  already scope-filtered result, and the answer reports `page`, `pages`, `total`, and `count`.
- `channels.browse`, `channelmembers.list`, and `users.list` page kChat's own `page` and `per_page`;
  `has_more` is true when kChat's page was full. Dropped channels or users can make `count` lower than the
  limit. `users.search` takes only `limit` and has no further page.
- `messages.list` pages kChat's own `GetPostsForChannel` pagination, newest first, and reports `has_more`
  from kChat's `has_next`.
- `messages.search` and `files.search` pass `page` and `limit` to kChat's own search and read no further
  page; `has_more` is true when kChat's page was full, and `count` may be lower because hits outside the
  reachable channels are dropped. kChat documents the paging of its search as working only with
  Elasticsearch. `channels.search` has no paging. Searches never include archived channels.
- `direct.list` reads kChat's complete channel array of the team, keeps the direct and group channels inside
  the allow-list, and proves only the requested window: one batched user check and one member read per
  group channel, which is why `limit` is at most 50. A channel that fails is left out, so `count` may be
  lower than `limit`; `has_more` reports a further window. When kChat lists no direct or group channel
  under the team, the result is empty; a channel stays reachable by its `channel_id` regardless.
- `messages.thread` pages kChat's `GetPostThread` pagination with an opaque `cursor`, kChat's `next_post_id`
  passed back unchanged; the answer reports `has_more` and, while it is true, the next `cursor`.

## Confirmation and unclear results

All confirmed tools require confirmation in their own request and change kChat with exactly one request,
sent after the binding reads: Qatlas never repeats it automatically. An answer that does not match what was
addressed (another channel type or participants for an open, another post or channel for an edit, no `OK`
status for a pin, another user, post, or emoji for a reaction) is an unreadable answer. A failure whose
request may nonetheless have reached kChat, such as a timeout, a connection reset, or a 5xx response, says
so in its message and is reported as-is; the caller decides whether to check before repeating it, never
Qatlas on its own.

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

Every scope refusal is an invalid request, never a provider error, so it is never mistaken for a missing
channel or message. This covers a `channel_id` or `post_id` outside the allow-list, one the live check
finds in another team or in a direct or group channel with a participant outside the bound teams, a deleted
post, a reply whose root belongs to a different channel, an open or member add naming a user outside the
bound teams or the channel's team, a role outside the two allowed, and an open on a connection with a
channel allow-list. None names the refused user or channel.

## Untrusted data

Team, channel, message, and user content come from the instance and are untrusted data. Qatlas normalises
them into a stable envelope and never renders them, follows a link inside them, or executes anything
derived from them, including a message's own text.

## Boundary

This provider reaches kChat alone. Infomaniak kDrive, Mail, and CalDAV/CardDAV are separate Infomaniak
products with their own authentication, none of them the token this provider uses; see the `infomaniakdrive`
provider's documentation for why each is its own sibling provider. Within kChat, this provider offers no
team management, no archiving, deletion, or visibility change of a channel, no membership change of a direct
or group channel, no user change, no profile picture, no preview or thumbnail, no custom emoji catalog, no
removal of another user's reaction, and no webhook configuration. An edit changes only a message's text,
never its attachments, pin state, or properties.

## Live test scenario

A live test against a real kChat instance checks, in order:

- Team and channel listings match the allow-lists and what the token belongs to; searches show no hit from
  a channel outside the allow-list, a direct message, or another team.
- Channel create and update work without a channel allow-list and are refused with one; member add, list,
  role, and remove work on connections that list the tools; a user only in another team is refused.
- Message list ends with `has_more` false on the last of two pages; one confirmed send appears exactly once;
  an upload is sent by `file_id` without `text`; an already attached file, another user's upload, and an
  upload for another channel are refused before posting.
- A reply threads under its root; pin and unpin show in `pins.list` without other channels' messages; an
  edit changes only text and edit time; deleting the reply keeps the root; reactions add, list, remove.
- A download matches `size`, and a storage-host redirect is a `provider-error`.
- A user only in another team is refused by `users.get` and `users.status` and absent from list and search.
- Direct and group channels open and take messages; `direct.list` shows whether kChat lists them under the
  team at all; a direct channel with a user only in another team is refused.
- Operations against a channel or post outside the targets, or a team the token cannot reach, are refused
  by the live check before anything changes.
- Which token sources an instance accepts, including a personal profile token.
