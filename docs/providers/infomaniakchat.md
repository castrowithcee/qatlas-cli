---
description: >
  Describes the Infomaniak kChat provider: token setup, the team and channel allow-list, the team, channel,
  message, thread, search, reaction, attachment, and user reads, the attachment download, the user scope
  check, pagination contracts, the confirmed channel create and update, send, edit, delete, and reaction tools
  and their unclear-result contract, redirect handling, and the boundary to kDrive, Mail, and CalDAV/CardDAV.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-09
---

# Infomaniak kChat

Infomaniak kChat is Infomaniak's Mattermost-compatible team chat product. This provider binds one kChat
instance and one or more of its teams, optionally narrowed to specific channels of them: it lists the bound
teams and channels, shows channel details and the public channels of a team, creates one confirmed channel
and changes a channel's display name, purpose, header, or handle, reads channel messages and threads page by
page, and sends or replies with exactly one confirmed message, reads, edits, and deletes single messages,
reads, adds, and removes reactions, lists the attachments of a message and downloads one to a released local
directory, searches messages, files, and public channels of a bound team, and reads, lists, and searches the
users of the bound teams and their presence, all through the instance's own `/api/v4/...` REST surface.
kChat renders Markdown and mentions such as `@channel` in a message; Qatlas sends the text as written. It
manages no team or membership, neither archives nor changes the visibility of a channel, changes no user,
sends no direct message, uploads no file, offers no previews or thumbnails, offers no custom emoji catalog,
and configures no webhook.

## Configuration

Unlike kDrive, kChat has no shared Infomaniak-wide API root: every kChat team has its own instance host,
always `https://TEAM.kchat.infomaniak.com`, the team's own name as the one DNS label directly below
`kchat.infomaniak.com` (confirmed by Infomaniak's own kChat MCP server,
[Infomaniak/mcp-server-kchat](https://github.com/Infomaniak/mcp-server-kchat), which builds every one of
its own requests from exactly that host). `base_url` must be exactly that shape: `https`, no port, no path,
no user, no query, and no fragment. Qatlas refuses any other host, label count, or scheme before a secret is
read, normalises the host to lowercase, and never follows a redirect away from it.

The credential provides `token`, sent as a bearer token: an Infomaniak Manager API token with the kChat scope
(valid account-wide), or a bot token created in the kChat interface, as the official `mcp-server-kchat`
README describes. A personal token from the kChat profile is not a documented source; it is usable only
where it has been verified live against an instance. Such a token can reach every team, and every channel of
it, its owner can reach, which matters for a person who works across several customers' teams: the
connection target, not the token, decides which teams and channels are reachable.

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
| `channel/CHANNEL_ID` | one channel of a bound team; optional, repeatable |

```yaml
connections:
  support-team:
    service: kchat
    credential: kchat-bot
    targets: [team/abcdEfgh12345678901234ab, channel/chanIdEfgh1234567890abcd]
```

Without a `channel/CHANNEL_ID` target, every channel of the bound teams the token can reach is reachable.
With one or more, only those channels are. A `channel_id` argument, a reply's `root_id`, and a `post_id` go
through the same two checks `infomaniakdrive` applies to a `drive_id`:

1. A malformed ID, and a `channel_id` outside a configured channel allow-list, are refused locally, as an
   invalid request, before any secret is read or any request is sent.
2. `infomaniakchat.messages.list`, `infomaniakchat.messages.send`, and `infomaniakchat.users.list` and
   `.search` with a `channel_id` then confirm with kChat's own channel detail endpoint
   (`GET /api/v4/channels/{channel_id}`) that the channel actually belongs to one of the bound teams, in one
   extra request. The allow-list is local configuration a person wrote; it is never trusted on its own,
   because the same token can belong to teams of several customers, and a channel named in an allow-list is
   not proof of which team it really belongs to. A channel of another team, or a check that failed or came
   back unreadable, aborts the request right there; there is no silent fallback to the message endpoint.

`channels.get` takes exactly one form, `channel_id` or `team_id` with `name`. The team of the second form is
checked locally; whichever form was used, the channel kChat answers with must belong to a bound team and be
inside the channel allow-list, or the call is an invalid request. `channels.browse` lists only the public
channels of a bound team and drops channels of other teams and outside the allow-list. `channels.update` binds
its channel with the live channel check first and refuses direct and group channels and archived channels.
`channels.create` works only in a bound team and only on a connection without a channel allow-list, because a
channel that does not exist yet cannot be inside one; the refusal is local, before any secret is read.

A `post_id` (`messages.thread`, `messages.get`, `messages.update`, `messages.delete`, and the `reactions` tools) is
bound through its channel: Qatlas reads the post, refuses an answer for another post or a deleted post, applies the
channel allow-list to the post's channel, and runs the live channel check above, all before any detail is returned
or any change is sent. A refusal never names the foreign target and never carries message content.

A reply additionally confirms, by reading the named `root_id` itself, that the root post belongs to the same
`channel_id` before anything is sent. Rejecting an out-of-scope channel or root post never carries the
message text into an error or a log.

A file (`files.info`, `files.download`) is bound only through the `post_id` kChat reports in its info: Qatlas
reads the info first, refuses a file without a `post_id` or a deleted file, and then applies the post binding
above, all before any content is requested. `messages.files` binds its `post_id` the same way.

A user is reachable when it is the token's own user (`GET /api/v4/users/me`) or a current member of a bound
team. Qatlas proves this live with `POST /api/v4/teams/{team_id}/members/ids`, one request per bound team
and only until every user is proven; a member who has left the team does not count. `users.get` refuses an
unreachable user as an invalid request without naming it, and `users.status` checks all its IDs before the
status request, so one foreign ID refuses the whole call. `users.list` and `users.search` drop unreachable
users from their answer. A `username` is read first and bound through the ID kChat reports before anything is
returned; an unknown username is refused exactly like a foreign one.

The searches (`messages.search`, `files.search`, `channels.search`) take a bound `team_id`, checked locally
before any secret is read. Qatlas reads the token's channels of that team (`GET
/api/v4/users/me/teams/{team_id}/channels`) and keeps only live channels of that team inside the channel
allow-list, never direct or group channels; then it sends one search request and drops every hit outside
that set, hits of other teams included. A file hit without a `channel_id` is dropped. Because the set comes
from the token's own channels, a public channel the token has not joined yields no hit, and
`channels.search` returns only public channels of that set.

A successful `qatlas connection test` reads the teams of the current token: it proves the token is accepted,
not that every team or channel on a narrower allow-list exists, is reachable, or actually belongs to it.

## Tools

| Tool | Effect | Reads or does |
| --- | --- | --- |
| `infomaniakchat.teams.list` | read | the bound teams the token is a member of, page by page |
| `infomaniakchat.channels.list` | read | the token's channels of one bound team, filtered to the channel allow-list, page by page |
| `infomaniakchat.channels.get` | read | one channel with header, member count, creation time, and archive state |
| `infomaniakchat.channels.browse` | read | the public channels of one bound team, page by page, members or not |
| `infomaniakchat.channels.search` | read | public channels of one bound team matching a `term`, limited to the token's own reachable channels |
| `infomaniakchat.channels.create` | create, confirmed | one public or private channel in a bound team |
| `infomaniakchat.channels.update` | update, confirmed | display name, purpose, header, or handle of one channel |
| `infomaniakchat.messages.list` | read | the messages of one channel this connection may reach, newest first, page by page |
| `infomaniakchat.messages.thread` | read | one message and the rest of its thread, page by page |
| `infomaniakchat.messages.get` | read | one message by `post_id` |
| `infomaniakchat.messages.search` | read | messages of one bound team matching `terms`, only from reachable channels, page by page |
| `infomaniakchat.messages.send` | create, confirmed | exactly one message, or, with `root_id`, one reply |
| `infomaniakchat.messages.update` | update, confirmed | the text of one message; nothing else of the post changes |
| `infomaniakchat.messages.delete` | delete, confirmed, tools list only | one message |
| `infomaniakchat.messages.files` | read | the metadata of the live files attached to one message, at most 50 |
| `infomaniakchat.files.info` | read | the metadata of one file attached to a reachable message |
| `infomaniakchat.files.download` | read, writes a local file | one attached file to `local_path` |
| `infomaniakchat.files.search` | read | file attachments of one bound team matching `terms`, only from reachable channels, page by page |
| `infomaniakchat.reactions.list` | read | the reactions of one message, page by page |
| `infomaniakchat.reactions.add` | create, confirmed | one reaction of the token's own user |
| `infomaniakchat.reactions.remove` | delete, confirmed, tools list only | one reaction of the token's own user |
| `infomaniakchat.users.get` | read | one reachable user by `user_id` or `username` (exactly one) |
| `infomaniakchat.users.list` | read | the users of one bound team or one channel, page by page |
| `infomaniakchat.users.search` | read | users of one bound team or channel matching a `term` |
| `infomaniakchat.users.status` | read | the presence of 1 to 100 reachable users |

The tools sort into the groups `teams`, `channels`, `messages`, `files`, `reactions`, and `users`. The reads
need no confirmation; channel create and update, send, update, delete, and the reaction changes always do,
through the same confirmation mechanism every other confirmed Qatlas tool uses. The terminal editor starts a
new connection on the setup profile `read`, which ticks the read tools, the channel, user, and attachment
tools and the three searches included; `messaging` adds `messages.send`, `messages.update`, and
`reactions.add` for a connection that should also post, edit, and react; `channel-admin` reads teams and
channels and adds `channels.create` and `channels.update`.

`channels.create` takes `team_id`, `name`, `display_name`, `type` (`O` public or `P` private), and optionally
`purpose` and `header`; `channels.update` takes `channel_id` and at least one of `display_name`, `purpose`,
`header`, and `name`, and sends only the given fields. Lengths and the `name` form are the tools' input
schemas. Direct and group channels are never created or changed.

`infomaniakchat.messages.delete` is in no profile and is offered only to a connection whose tools list names
it. kChat soft-deletes the post, which users cannot restore, and deleting a thread root also removes its
replies. Whether a token may edit or delete a message of another author is kChat's decision
(`edit_others_posts`, `delete_others_posts`); a refusal is `permission`.

Reactions are always those of the token's own user. Its ID comes from `GET /api/v4/users/me` in the same
call, never from an argument, so reactions of other users cannot be added or removed. `emoji_name` is 1 to 64
characters of `a-z`, `0-9`, `_`, `+`, and `-`, checked locally before any secret is read.
`infomaniakchat.reactions.remove` is in no profile and is offered only to a connection whose tools list names
it. Adding an existing reaction changes nothing.

`files.download` follows the local file contract of `infomaniakdrive.files.download`: it writes only inside a
directory the connection releases for writing (`files: write`), replaces an existing file only with
confirmation, and leaves no file when the transfer is incomplete or its size differs from the size kChat
reported in the file's info. A transfer above the fixed Qatlas file limit is refused. The result carries only
`file_id`, `name`, `size`, and `sha256`, never content. The content request is the fixed
`GET /api/v4/files/{file_id}`; kChat's answer is streamed to the file and never decoded or returned. A
redirect, for example to a storage host, is a `provider-error` and is not followed. Attachment results carry
the data sensitivity `infomaniak-kchat-files`; file names and media types are untrusted data, and
`include_deleted` is never sent.

A search `terms` is 1 to 512 characters without control characters; kChat's `from:`, `in:`, and `ext:` syntax
is allowed because the reachable-channel filter applies afterwards. The term is never part of an error.

Users are output through a fixed field allow-list: `id`, `username`, `first_name`, `last_name`, `nickname`,
`position`, `is_bot`, `deleted`, and `email` only when kChat reports one to the token. Roles, notification
settings, properties, time zone, authentication service, and credential or MFA fields are never decoded.
`users.list` and `users.search` need exactly one of `team_id` (a bound team) or `channel_id`; an
instance-wide list is not offered. `username` is 1 to 64 characters of `a-z`, `0-9`, `.`, `_`, and `-`, and
a search `term` is 1 to 64 characters. `users.search` is a `POST` that changes nothing. `users.status`
reports `user_id`, `status`, `manual`, and `last_activity_at`; entries for IDs that were not asked are
dropped. User results carry the data sensitivity `infomaniak-kchat-people`.

## Pagination

Every list is bounded and paginated, and Qatlas never follows a further page on its own:

- `infomaniakchat.teams.list` and `infomaniakchat.channels.list` read kChat's own listing, which answers as
  one complete array with no pagination of its own, and page it themselves: `page` (1-based, default 1) and
  `limit` (1 to 200, default 50) select a window of the already scope-filtered result, and the answer reports
  `page`, `pages`, `total`, and `count`.
- `infomaniakchat.channels.browse` pages kChat's own `page` and `per_page`: `page` is 1-based, `per_page` is
  1 to 200, and `has_more` is true when kChat's page was full; channels dropped by the filters can make
  `count` lower than `per_page`.
- `infomaniakchat.messages.list` pages kChat's own `GetPostsForChannel` pagination directly: `page` (1-based)
  and `limit` select one page of the channel, newest first, and the answer reports `has_more` from kChat's
  own `has_next`.
- `infomaniakchat.messages.search` and `files.search` pass `page` (1-based) and `limit` (1 to 100, default 50)
  to kChat's own search and read no further page; `has_more` is true when kChat's page was full, and `count`
  may be lower than `limit` because hits outside the reachable channels are dropped. kChat documents the
  paging of its search as working only with Elasticsearch. `channels.search` has no paging. Searches never
  include archived channels.
- `infomaniakchat.reactions.list` reads kChat's complete reaction array for the post and pages it itself like
  the team listing, reporting only `user_id`, `emoji_name`, and `created_at` per reaction.
- `infomaniakchat.users.list` pages kChat's own `page` and `per_page`: `page` (1-based) and `limit` select one
  page, and `has_more` is true when the page was full; users outside the bound teams are dropped from it, so
  `count` may be lower than `limit`. `users.search` takes only `limit` (1 to 100) and has no further page.
- `infomaniakchat.messages.thread` pages kChat's own `GetPostThread` pagination: an opaque `cursor`, kChat's
  own `next_post_id` passed back unchanged, continues a thread forward; the answer reports `has_more` and,
  while it is true, a `cursor` for the next page.

## Confirmation and unclear results

`infomaniakchat.channels.create`, `channels.update`, `messages.send`, `.update`, `.delete`, `reactions.add`,
and `reactions.remove` require confirmation in their own request and change kChat with exactly one request,
sent after the binding reads:
Qatlas never repeats it automatically. An edit answered with another post or channel, or a reaction answered
with another user, post, or emoji than the one addressed, is an unreadable answer. A failure whose request
may nonetheless have reached kChat, such as a timeout, a connection reset, or a 5xx response, says so in its
message and is reported as-is; the caller decides whether to check before repeating it, never Qatlas on its
own.

## Errors

Errors keep stable classes and never carry the token, a message's text, or a raw provider response body,
which may echo request content back at the caller:

| Class | Cause |
| --- | --- |
| `auth` | kChat rejected the token |
| `permission` | this token may not perform the operation; check its rights on the team or channel in kChat |
| `not-found` | kChat does not hold the resource, or does not show it to this token |
| `rate-limited` | kChat rate-limited the request; kChat publishes no fixed per-instance budget, so Qatlas applies no proactive spacing of its own and only honours a reported `Retry-After` |
| `timeout` | kChat did not answer in time |
| `unreachable` | kChat could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, or malformed |
| `provider-error` | every other rejection, including a redirect on an endpoint that must not answer with one |

A `channel_id` or `post_id` outside the connection's allow-list, one the live check finds belongs to another
team, a deleted post, or a reply whose root post belongs to a different channel, is an invalid request,
never a provider error, so a scope refusal is never mistaken for a missing channel or message.

## Untrusted data

Team, channel, message, and user content come from the instance and are untrusted data. Qatlas normalises them
into a stable envelope and never renders them, follows a link inside them, or executes anything derived from
them, including a message's own text.

## Boundary

This provider reaches kChat alone. Infomaniak kDrive, Mail, and CalDAV/CardDAV are separate Infomaniak
products with their own authentication, none of them the token this provider uses; see the `infomaniakdrive`
provider's documentation for why each is its own sibling provider rather than one shared Infomaniak provider.
Within kChat itself, this provider offers no team management, channel membership, visibility change,
archiving, or deletion, no user change, no direct message, no profile picture, no file upload, no preview or
thumbnail, no custom emoji catalog, no removal of another user's reaction, and no webhook configuration, and
an edit changes only a message's text, never its attachments, pin state, or properties.

## Live test scenario

A future live test against a real kChat instance should, in order: read `teams.list` and confirm the
configured team allow-list matches what the token actually belongs to; read `channels.list` of one bound
team and confirm the channel allow-list, when configured, is applied; read one channel with `channels.get`
by ID and by name, and browse the public channels across two pages; search messages, files, and channels and
confirm hits from a channel outside the allow-list, a direct message, and another team are absent; create a
disposable channel from a connection without a channel allow-list, change its header, and confirm the same
call is refused on a connection with one; read `messages.list` of one channel across two pages and confirm
`has_more` turns false on the last one; send one confirmed message to a disposable test channel and confirm
exactly one message appears; send one confirmed reply with `root_id` and confirm it threads under the
original message; read it with `messages.get`; edit its text and confirm only the text and the edit time
change; delete the reply from a connection that lists the delete tool and confirm the root stays; add a
reaction, read it with `reactions.list`, and remove it from a connection that lists the remove tool; list
the attachments of a message with a file, download one into a released directory, and confirm the written
size matches `size` and that a storage-host redirect, if kChat answers with one, is reported as
`provider-error`; read a bound team's users and their presence, search one by name, and confirm a user who
is only in another team of the instance is refused by `users.get` and `users.status` and absent from the
list and search; attempt a send, get, edit, delete, reaction, and file download against a channel or post
outside the connection's targets and confirm each is refused before anything changes; attempt the same with
a channel or team the credential's token cannot actually reach, to confirm the live check reports it, not a
stale allow-list; and confirm which token sources an instance accepts, including a personal profile token.
