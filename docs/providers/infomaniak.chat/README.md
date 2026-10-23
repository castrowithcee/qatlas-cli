---
description: >
  Describes the Infomaniak kChat provider: token setup, the team and channel allow-list, the binding of direct
  and group channels, posts, files, and users, the instance-wide connection form, the local file contracts,
  pagination, the confirmed changes and their unclear-result contract, redirect handling, and the boundary to
  kDrive, Mail, and CalDAV/CardDAV. The tool groups are described on their own pages.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-23
---

# Infomaniak kChat

kChat is Infomaniak's Mattermost-compatible team chat product. This provider binds one kChat instance and one
or more of its teams, optionally narrowed to channels, and reads and changes channels and their members,
direct and group channels, messages, threads, followed threads, read state, reactions, pins, flags,
attachments, users, the own sidebar categories, channel notifications, webhooks, and slash commands through
the instance's `/api/v4/...` REST API; an instance-wide connection also reaches bot accounts. kChat renders
Markdown and mentions such as `@channel` in a message; Qatlas sends the text as written.

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
    provider: infomaniak.chat
    base_url: https://acme-support.kchat.infomaniak.com

credentials:
  kchat-bot:
    provider: infomaniak.chat
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
and a `post_id` go through the same two checks `infomaniak.drive` applies to a `drive_id`:

1. A malformed ID, and a `channel_id` outside a configured allow-list, are refused locally, as an invalid
   request, before any secret is read or any request is sent.
2. Every tool with a `channel_id` then confirms live with `GET /api/v4/channels/{channel_id}` that the
   channel belongs to one of the bound teams or, for a direct or group channel, that its participants are
   bound as described below. The allow-list is local configuration a person wrote and is never trusted on
   its own: the same token can belong to teams of several customers. A channel of another team, or a check
   that failed or came back unreadable, aborts the request; there is no silent fallback.

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

The searches (`messages.search`, `files.search`, `channels.search`), `threads.list`, and `flagged.list` (which
takes exactly one of this `team_id` and a `channel_id`) take a bound `team_id`, checked locally. Qatlas reads
the token's channels of that team (`GET /api/v4/users/me/teams/{team_id}/channels`), keeps only live channels
inside the allow-list and never direct or group channels, sends one request, and drops every hit or thread
outside that set, including a file hit without a `channel_id` and a thread whose root is deleted. Because the
set comes from the token's own channels, a public channel the token has not joined yields no hit.
`insights.top` applies it to ranked channels and threads and drops unreachable users; no thread text shows.

The `bots`, `emoji`, and `roles` tools need an instance-wide connection: all token teams bound, no `channel/`
target. A channel target refuses before any secret is read; otherwise each call reads `/api/v4/users/me/teams`
and refuses, naming no team, on an unbound, empty, or unreadable answer, so a later-joined team locks them.

A successful `qatlas connection test` proves only that the token is accepted, not that a team or channel
of the allow-list exists or is reachable.

## Cross-group behaviour

- A `post_id` (`messages.thread`, `.get`, `.update`, `.delete`, `.pin`, `.unpin`, `.flag`, `.unflag`,
  `.remind`, `.mark-unread`, and the `reactions` tools) is bound through its channel: Qatlas reads the post,
  refuses another or a deleted post, applies the allow-list, and runs the live check first.
- `status.set`, `custom-status.set`, `.clear`, `profile.update`, reactions, flags, categories, notifications,
  and thread following always address the token's own user, read from `GET /api/v4/users/me` or `users/me` in
  the path and never from an argument; the status and profile act for it in every team of the instance (for
  the bot with a bot token). `profile.update` sends only `nickname`, `first_name`, `last_name`, `position`.
- A search term may use kChat's `from:`, `in:`, and `ext:` syntax; the reachable-channel filter applies
  afterwards. The term is never part of an error.

## Choosing targets

The target picker offers two levels: `team`, then `channel` below the chosen team. It works on a draft
connection that has no team yet, and it lists what the token reaches, not only the current targets. Teams are
the token's teams; channels are the live, non-archived channels of the chosen team the token is a member of.
Direct and group channels belong to no team and are not offered. Names are untrusted data. Both lists are paged
by Qatlas from kChat's single answer; a search text on the channel level uses `channels.search`, so it finds
public channels only, while the list also shows private ones.

A channel URL `https://TEAM.kchat.infomaniak.com/TEAM/channels/NAME` on the connection's own instance is turned
into `team/TEAM_ID` and `channel/CHANNEL_ID`: the team handle is looked up among the token's teams, the channel
handle with `GET /api/v4/teams/{team_id}/channels/name/{name}`. Any other host, form, or a team or channel the
token does not show gives nothing. Reading a URL costs two requests; every list read counts like any other.

A connection with a channel target is not offered the tools that are refused with one: the `bots`, `emoji`,
`roles`, and `commands` tools, `channels.create`, `direct.open`, and `group-messages.open`.

## Tools

Every tool ID starts with `infomaniak.chat.`; the tool group is the first column:

| Group | Reads | Confirmed changes | Only with a tools list |
| --- | --- | --- | --- |
| [`teams`](teams.md) | `teams.list`, `.get`, `team-members.list`, `insights.top` | | `team-members.roles` |
| [`channels`](channels.md) | `channels.list`, `.get`, `.browse`, `.search`, `.unread`, `archived-channels.list`, `channel-members.list`, `categories.list` | `channels.create`, `.update`, `.mark-read`, `channel-members.add`, `categories.create`, `.update`, `channel-notifications.update` | `channels.archive`, `.restore`, `.privacy`, `channel-members.remove`, `.roles`, `categories.delete` |
| [`messages`](messages.md) | `messages.list`, `.thread`, `.get`, `.files`, `.search`, `direct.list`, `pins.list`, `flagged.list` | `direct.open`, `group-messages.open`, `messages.send`, `.update`, `.pin`, `.unpin`, `.flag`, `.unflag`, `.remind`, `.mark-unread` | `messages.delete` |
| [`files`](files.md) | `files.info`, `.search`, `.download` (writes a local file) | `files.upload` (reads a local file) | |
| [`threads`](threads.md) | `threads.list`, `.get` | `threads.follow`, `.unfollow`, `.mark-read` | |
| [`reactions`](reactions.md) | `reactions.list` | `reactions.add` | `reactions.remove` |
| [`users`](users.md) | `users.get`, `.list`, `.search`, `.status`, `user-groups.list` | `status.set`, `custom-status.set`, `.clear`, `profile.update` | |
| [`integrations`](integrations.md) | | | `incoming-webhooks.*`, `outgoing-webhooks.*`, `commands.*` |
| [`administration`](administration.md) | `emoji.list` | | `bots.list`, `.get`, `.update`, `.disable`, `.enable`, `roles.get` (a `POST` that changes nothing), `emoji.create` (reads a local file), `.delete` |

The reads need no confirmation; every other tool always does. The tools of the last column are in no profile
and offered only to a connection whose tools list names them; the role tools alter rights, and
`channels.archive` is kChat's channel delete, which keeps the channel restorable.

The terminal editor starts a new connection on the setup profile `read` (all read tools). `messaging` adds
the opens, sends, edits, pins, flags, reminders, read marks, uploads, reactions, thread following, category
and notification changes, and the own status tools; `channel-admin` adds `channels.create`, `.update`,
and `channel-members.add`. `profile.update`, the integration, bot, and emoji-writing tools are in no profile.

## Pagination

Every list is bounded and paginated, and Qatlas never follows a further page on its own:

- `teams.list`, `channels.list`, `reactions.list`, `pins.list`, and `commands.list` read kChat's complete
  array, which has no pagination of its own, and page it themselves: `page` (1-based) and `limit` select a
  window of the already scope-filtered result, and the answer reports `page`, `pages`, `total`, and `count`.
- `channels.browse`, `archived-channels.list`, the webhook lists, `bots.list`, `emoji.list`,
  `channel-members.list`, `team-members.list`, and `users.list` page kChat's `page`/`per_page`; `has_more` is
  true for a full page; dropped entries can lower `count`. `users.search` takes only `limit`, one page.
- `messages.list` (`GetPostsForChannel`, newest first) and `insights.top`: `has_more` is kChat's `has_next`.
- `messages.search`, `files.search`, `flagged.list`, and `threads.list` pass `page` and `limit` (at most 100)
  to kChat's own paging and read no further page; `has_more` is true when kChat's page was full, and `count`
  may be lower because hits outside the reachable channels are dropped. kChat documents search paging as
  Elasticsearch-only. `channels.search` has no paging; searches never include archived channels.
- `direct.list` reads kChat's complete channel array of the team, keeps the direct and group channels inside
  the allow-list, and proves only the requested window (`limit` at most 50). A failing channel is left out,
  so `count` may be lower; `has_more` reports a further window. A channel kChat lists under no team stays
  reachable by its `channel_id`.
- `messages.thread` pages kChat's `GetPostThread` pagination with an opaque `cursor`, kChat's `next_post_id`
  passed back unchanged; the answer reports `has_more` and, while it is true, the next `cursor`.

## Confirmation and unclear results

All confirmed tools require confirmation in their own request and change kChat with exactly one request, sent
after the binding reads, never repeated automatically. An answer that does not match what was addressed, such
as another post or thread or a missing `OK` status, is an unreadable answer. A failure whose request may have
reached kChat, such as a timeout, a reset, or a 5xx response, says so; only the caller may repeat it.

## Errors

Errors keep stable classes and never carry the token, a message's text, or a raw provider response body:

| Class | Cause |
| --- | --- |
| `auth` | kChat rejected the token |
| `permission` | this token may not perform the operation; check its rights on the team or channel in kChat |
| `not-found` | kChat does not hold the resource, or does not show it to this token; HTTP 404 of `insights.top` means the instance offers no insights |
| `rate-limited` | kChat rate-limited the request; Qatlas applies no proactive spacing and only honours `Retry-After` |
| `timeout` | kChat did not answer in time |
| `unreachable` | kChat could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, or malformed |
| `provider-error` | every other rejection, such as HTTP 501 of `insights.top`, including a redirect on an endpoint that must not answer with one |

Every refusal under [Scope](#scope), and every argument outside its fixed set, is an invalid request, never a
provider error. It names no refused target or hook ID and carries no message text into an error or a log.

## Untrusted data

Team, channel, message, category, user, bot, webhook, and command content is untrusted data: Qatlas normalises
it and never renders it, follows a link in it, or executes anything derived from it.

## Boundary

This provider reaches kChat alone. Infomaniak kDrive, Mail, and CalDAV/CardDAV are separate products with
their own authentication, not this token; see the `infomaniak.drive` provider's documentation for why each is
its own sibling provider. Within kChat, this provider offers no team creation, change, or deletion, no adding
or removing of team members, no invitation, no permanent deletion, move, scheme, or moderation change of a
channel, no membership change of a direct or group channel, no change of another user or of other profile
fields, no profile picture, no preview or thumbnail, no custom emoji image, no removal of another user's
reaction, no read state of another user, no new webhook, command, bot, or bot token, no command execution, and
no change of a webhook or command beyond its texts. An edit changes only a message's text, never its
attachments, pin state, or properties.

## Live test scenario

A live test against a real kChat instance checks, in order:

- Listings match the allow-lists and the token's teams and channels; searches and `insights.top` show nothing
  from a channel outside the allow-list, a direct message, or another team.
- Channel create and update work only without a channel allow-list; member tools refuse a user only in
  another team. A disposable channel switches to private, is archived, shows in `archived-channels.list`,
  and is restored; the default channel's refusal is a provider error class.
- Message list ends with `has_more` false on the last of two pages; one confirmed send appears exactly once;
  an upload is sent by `file_id` without `text`; an attached file, another user's upload, and an upload for
  another channel are refused before posting; a download matches `size`; a storage redirect is refused.
- A reply threads under its root; pin and unpin show in `pins.list` without other channels' messages; an edit
  changes only text and edit time; deleting the reply keeps the root; reactions add, list, remove.
- Follow, unfollow, flag, remind, and read marks change only own state; foreign posts and threads, replies,
  and a future read mark are refused; `flagged.list` shows kChat's answer shape; a reminder fires on time.
- Webhooks and slash commands list without those of other teams or outside the allow-list, channelless
  outgoing webhooks included; a disposable one changes its texts only, then is deleted.
- Bot and emoji tools are refused with a channel target or an unbound token team; `bots.list` and `emoji.list`
  list; a bot is renamed, changes only its description (with or without `username`), and is disabled and
  enabled; an emoji is created from a PNG, listed, and deleted, which confirms the multipart format.
- Status, custom status, and profile changes affect only the token's own user and keep other fields; a user
  only in another team is refused by `users.get` and `users.status` and absent from list and search.
- A custom category is created, filled, renamed, and deleted; channels outside the allow-list stay in it;
  `favorites` refuses a rename; notification changes touch only the given fields.
- Direct and group channels open and take messages; `direct.list` shows whether kChat lists them under the
  team; a direct channel with a user only in another team is refused.
- Operations outside the targets or in an unreachable team are refused before anything changes; which token
  sources an instance accepts, including a personal profile token.
