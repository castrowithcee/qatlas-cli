---
description: >
  Describes the Infomaniak kChat provider: token setup, the team and channel allow-list, the team, channel,
  message, and thread reads, pagination contracts, the confirmed send, edit, and delete tools and their
  unclear-result contract, redirect handling, and the boundary to kDrive, Mail, and CalDAV/CardDAV.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-09
---

# Infomaniak kChat

Infomaniak kChat is Infomaniak's Mattermost-compatible team chat product. This provider binds one kChat
instance and one or more of its teams, optionally narrowed to specific channels of them: it lists the bound
teams and channels, reads channel messages and threads page by page, and sends or replies with exactly one
confirmed message, reads, edits, and deletes single messages, all through the instance's own `/api/v4/...`
REST surface. kChat renders Markdown and mentions such as `@channel` in a message; Qatlas sends the text as
written. It manages no channel or team, uploads no file, adds no reaction, and configures no webhook.

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
2. `infomaniakchat.messages.list` and `infomaniakchat.messages.send` then confirm with kChat's own channel
   detail endpoint (`GET /api/v4/channels/{channel_id}`) that the channel actually belongs to one of the
   bound teams, in one extra request. The allow-list is local configuration a person wrote; it is never trusted on its own,
   because the same token can belong to teams of several customers, and a channel named in an allow-list is
   not proof of which team it really belongs to. A channel of another team, or a check that failed or came
   back unreadable, aborts the request right there; there is no silent fallback to the message endpoint.

A `post_id` (`messages.thread`, `messages.get`, `messages.update`, `messages.delete`) is bound through its
channel: Qatlas reads the post, refuses an answer for another post or a deleted post, applies the channel
allow-list to the post's channel, and runs the live channel check above, all before any detail is returned
or any change is sent. A refusal never names the foreign target and never carries message content.

A reply additionally confirms, by reading the named `root_id` itself, that the root post belongs to the same
`channel_id` before anything is sent. Rejecting an out-of-scope channel or root post never carries the
message text into an error or a log.

A successful `qatlas connection test` reads the teams of the current token: it proves the token is accepted,
not that every team or channel on a narrower allow-list exists, is reachable, or actually belongs to it.

## Tools

| Tool | Effect | Reads or does |
| --- | --- | --- |
| `infomaniakchat.teams.list` | read | the bound teams the token is a member of, page by page |
| `infomaniakchat.channels.list` | read | the token's channels of one bound team, filtered to the channel allow-list, page by page |
| `infomaniakchat.messages.list` | read | the messages of one channel this connection may reach, newest first, page by page |
| `infomaniakchat.messages.thread` | read | one message and the rest of its thread, page by page |
| `infomaniakchat.messages.get` | read | one message by `post_id` |
| `infomaniakchat.messages.send` | create, confirmed | exactly one message, or, with `root_id`, one reply |
| `infomaniakchat.messages.update` | update, confirmed | the text of one message; nothing else of the post changes |
| `infomaniakchat.messages.delete` | delete, confirmed, tools list only | one message |

The tools sort into the groups `teams`, `channels`, and `messages`. The five reads need no confirmation; send,
update, and delete always do, through the same confirmation mechanism every other confirmed Qatlas tool
uses. The terminal editor starts a new connection on the setup profile `read`, which ticks the read tools;
`messaging` adds `messages.send` and `messages.update` for a connection that should also post and edit.

`infomaniakchat.messages.delete` is in no profile and is offered only to a connection whose tools list names
it. kChat soft-deletes the post, which users cannot restore, and deleting a thread root also removes its
replies. Whether a token may edit or delete a message of another author is kChat's decision
(`edit_others_posts`, `delete_others_posts`); a refusal is `permission`.

## Pagination

Every list is bounded and paginated, and Qatlas never follows a further page on its own:

- `infomaniakchat.teams.list` and `infomaniakchat.channels.list` read kChat's own listing, which answers as
  one complete array with no pagination of its own, and page it themselves: `page` (1-based, default 1) and
  `limit` (1 to 200, default 50) select a window of the already scope-filtered result, and the answer reports
  `page`, `pages`, `total`, and `count`.
- `infomaniakchat.messages.list` pages kChat's own `GetPostsForChannel` pagination directly: `page` (1-based)
  and `limit` select one page of the channel, newest first, and the answer reports `has_more` from kChat's
  own `has_next`.
- `infomaniakchat.messages.thread` pages kChat's own `GetPostThread` pagination: an opaque `cursor`, kChat's
  own `next_post_id` passed back unchanged, continues a thread forward; the answer reports `has_more` and,
  while it is true, a `cursor` for the next page.

## Confirmation and unclear results

`infomaniakchat.messages.send`, `.update`, and `.delete` require confirmation in their own request and
change kChat with exactly one request, sent after the binding reads: Qatlas never repeats it automatically.
An edit answered with another post or channel than the one addressed is an unreadable answer. A failure
whose request may nonetheless have reached kChat, such as a timeout, a connection reset, or a 5xx response,
says so in its message and is reported as-is; the caller decides whether to check the channel before
sending again, never Qatlas on its own.

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

Team, channel, and message content come from the instance and are untrusted data. Qatlas normalises them
into a stable envelope and never renders them, follows a link inside them, or executes anything derived from
them, including a message's own text.

## Boundary

This provider reaches kChat alone. Infomaniak kDrive, Mail, and CalDAV/CardDAV are separate Infomaniak
products with their own authentication, none of them the token this provider uses; see the
`infomaniakdrive` provider's documentation for why each is its own sibling provider rather than one shared
Infomaniak provider. Within kChat itself, this provider offers no channel or team creation, membership, or
administration, no file attachment, no reaction, and no webhook configuration, and an edit changes only a
message's text, never its attachments, pin state, or properties.

## Live test scenario

A future live test against a real kChat instance should, in order: read `teams.list` and confirm the
configured team allow-list matches what the token actually belongs to; read `channels.list` of one bound
team and confirm the channel allow-list, when configured, is applied; read `messages.list` of one channel
across two pages and confirm `has_more` turns false on the last one; send one confirmed message to a
disposable test channel and confirm exactly one message appears; send one confirmed reply with `root_id` and
confirm it threads under the original message; read it with `messages.get`; edit its text and confirm only
the text and the edit time change; delete the reply from a connection that lists the delete tool and confirm
the root stays; attempt a send, get, edit, and delete against a channel or post outside the connection's
targets and confirm each is refused before anything changes; attempt the same with a channel or team the
credential's token cannot actually reach, to confirm the live check reports it, not a stale allow-list; and
confirm which token sources an instance accepts, including a personal profile token.
