---
description: >
  Describes the Telegram provider: targets, chat binding, topic binding, link resolution, local files, signed references, credential, connection effects, setup profiles, and the tool group pages.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-22
---

# Telegram

A connection binds one bot token to its targets (`target` or `targets`). Every changing tool requires
confirmation; Telegram's own restrictions still apply. Tools are documented per group:

| Group | Page |
|---|---|
| Messages | [messages.md](messages.md) |
| Pins | [pins.md](pins.md) |
| Updates | [updates.md](updates.md) |
| Queries | [queries.md](queries.md) |
| Business | [business.md](business.md) |
| Stories | [stories.md](stories.md) |
| Chats | [chats.md](chats.md) |
| Invite links | [invite-links.md](invite-links.md) |
| Bot | [bot.md](bot.md) |
| Files | [files.md](files.md) |
| Media | [media.md](media.md) |
| Members | [members.md](members.md) |
| Users | [users.md](users.md) |
| Interactions | [interactions.md](interactions.md) |
| Stickers | [stickers.md](stickers.md) |
| Topics | [topics.md](topics.md) |
| Suggested posts | [suggested-posts.md](suggested-posts.md) |
| Games | [games.md](games.md) |
| Stars and gifts | [payments.md](payments.md) |

## Targets

A target is a chat (a numeric chat ID, also negative, or an `@username`), a forum topic
`CHAT_ID/topics/THREAD_ID` of a chat with a numeric ID, `bot`, or `business/<id>`.

`bot` unlocks only bot-wide tools and `business/<id>` only the tools of that one business connection; both
combine with chats, and a connection with only `bot` has no chat. A tool missing its target is refused before the
credential is read, and methods that accept only a numeric chat ID refuse an `@username` chat locally.

## Chat argument

The chat tools take an optional `chat` argument, the forward and copy tools also `from_chat`. It must equal a
bound chat target exactly: an `@username` never matches a numeric ID, and `bot` or `business/<id>` is no chat.
Without `chat`, the tool uses the one bound chat and refuses when the connection binds none or several. A chat
that is not bound is refused before the credential is read and before any request, without naming it. A chat
ID from an invocation argument or a Telegram response never becomes a target on its own.

## Topic targets

A topic target binds only its topic, and together with a chat target of the same chat the whole chat. In a chat
bound only through topics, the tools with `message_thread_id` that send or manage a topic stay in a bound topic:
an omitted one becomes the only bound topic, and `chat` may name the topic target. Another topic, the General
topic, and a reply are refused before the credential is read, since Telegram returns and quotes the replied
message. Every other chat tool and `from_chat` act on the whole chat or on a message whose topic is unknown without
a read, so they are refused there and not offered.

## Link resolution

The Bot API has no chat list, so setup offers none. Without a request, `https://t.me/<username>` (also with a
message number) becomes `@<username>`, `https://t.me/c/<id>/...` becomes `-100<id>`, and a forum message link
`https://t.me/c/<id>/<topic>/<message>` also yields `-100<id>/topics/<topic>`; other `t.me` paths yield none.

## Connection

Tool groups only sort tools for display. The base URL must be a plain `https` URL with a host and without user,
query, or fragment, with no exception for local addresses, and redirects are never followed. Errors never carry
Telegram's own error text.

The credential provides `bot-token`. Connection effects only reduce what Qatlas exposes and executes; they do
not broaden the bot's provider-side rights. A connection may instead select named tools with a `tools` list.
Mutations are never retried after an ambiguous network result.

## Local files

A tool that takes a `local_path` is offered only on a connection that releases a directory for it (`files`): for
reading to upload, for writing to download. A path outside it is refused before the credential is read, and an
upload's size is checked before the file is read. A local file is sent under a neutral name, so path and file
name stay local.

## Signed references

Identifiers that belong to no chat (a file, a callback query, a join request query) are returned only as signed
references, never as Telegram's raw identifier. A reference is bound to its target and the bot token, so rotating
the token invalidates earlier ones; it is signed, not secret. A tool that accepts one checks format and target
before the credential is read and the signature before any request.

## Setup profiles

The terminal editor starts a new connection on the setup profile `send`: the read tools expose incoming message
content, while a send reaches only a bound chat after confirmation. A profile is only a visible starting selection:
every tick can be changed before saving, and a saved connection never follows a profile.
