---
description: >
  Guides a user without special knowledge through setting up the Telegram provider: creating a bot with BotFather, adding its token to Qatlas, checking the connection, adding the bot to chats, the bot rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Telegram

This page shows how to connect Qatlas to Telegram, which rights the bot needs in a chat, and what to do when
Telegram refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Telegram provider](README.md) and the page of each tool group; this page names only the rights.

**The bot decides what is possible at all.** Telegram has no token with scopes: the bot token only proves which bot
you are, and what the bot may do is set separately in each chat (as administrator rights) or by the owner of a
business account. The rights you set in Qatlas (the connection's `tools`, `effects`, and `targets`) limit it further.
A call works only if both allow it. A connection can never do more than its bot, and a bot never widens the
connection.

## What you need

- A Telegram account, to create the bot and to add it to chats.
- A bot and its bot token from @BotFather. One connection uses one bot.
- Administrator rights for the bot in every group or channel where it should do more than send.
- For business tools: a business account that connects your bot and grants it rights; see
  [Business accounts](#business-accounts).
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable. It reaches
  `https://api.telegram.org` by default; see [Connection](README.md#connection).

## Create a bot

In Telegram, open the chat with @BotFather. Telegram changes its pages from time to time; look for these names.

1. Send `/newbot`. BotFather asks for a name and a username. The username must end in `bot` and cannot be changed
   later.
2. BotFather answers with the **bot token**, which has the form `<bot-id>:<secret>`: the bot's number, a colon, and
   a secret part of letters, digits, `-`, and `_`. Copy it. Anyone who has the token controls the bot, so store it
   nowhere but in Qatlas.
3. If the token leaks or is lost, send `/token` to BotFather to generate a new one, then store the new token in
   Qatlas (see below). Signed references issued with the old token stop working; see
   [Signed references](README.md#signed-references).

Settings that some tools depend on are made in BotFather as well:

| Setting | When you need it |
| --- | --- |
| `/setjoingroups` | The bot must be allowed to join groups. |
| `/setprivacy` | The bot must read all messages of a group; see [Privacy mode](#privacy-mode). |
| `/setinline` | The bot answers inline queries (`telegram.inline.answer`). |
| `/setinlinefeedback` | Updates about the results people choose are wanted. |
| `/newgame` | The bot sends a game (`telegram.games.send`). |

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `telegram`, the service, a credential, the
   scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `bot-token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$TELEGRAM_BOT_TOKEN" | qatlas credential set telegram-bot bot-token
```

`telegram-bot` is the name of the credential in your configuration and `bot-token` is its only secret. To replace a
token, run the same command again with the new token. Run `qatlas config validate` to check the configuration file.
It contacts no provider.

The targets name the chats the bot may act in; see [Targets](README.md#targets). Telegram offers no chat list, so you
give the chat ID, the `@username`, or a `t.me` link; see [Link resolution](README.md#link-resolution).

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Telegram accepted the token.
The test calls only the read-only `getMe` and never sends to a chat.

**A passing test does not mean every tool works.** It checks neither the chats nor the bot's rights there. Telegram
checks the rights again on every call. Compare the bot with [Rights per tool group](#rights-per-tool-group). A
failing test or call is explained under [Fixing errors](#fixing-errors).

## Add the bot to chats

- **Private chat:** the chat of the bot with a person; the tools for groups and channels do not apply there.
- **Group or supergroup:** add the bot as a member. For anything beyond sending and reading, make it an
  administrator and tick only the rights of [Rights per tool group](#rights-per-tool-group) for the tools you use.
- **Channel:** add the bot as an administrator; a bot posts and edits there only with the matching administrator
  right.
- **Forum topics:** the bot needs the rights of the supergroup; a topic target only narrows what Qatlas allows, see
  [Topic targets](README.md#topic-targets).

Telegram can suggest administrator rights to the person who adds the bot; the rights the bot requests can be read and
set with `telegram.default-admin-rights.get` and `.set`, see [Bot](bot.md#profile-and-default-rights). The person
adding the bot may change that list before confirming.

A group that becomes a supergroup gets a new chat ID; adjust the connection's target when Qatlas reports it.

### Privacy mode

By default a bot in a group sees only commands meant for it, replies to its own messages, and a few related items.
It always receives service messages, all messages of private chats, and all messages of channels where it is a
member. A bot that is an administrator of a group receives all messages. To read all messages without administrator
rights, disable privacy mode with `/setprivacy` in BotFather and add the bot to the group again; Telegram applies the
change only then. This matters for [Updates](updates.md): `telegram.updates.list` shows only what Telegram delivers.

### Business accounts

The owner of a business account connects your bot in Telegram and chooses its **business bot rights**. Qatlas
addresses such a connection with the target `business/<id>`; see [Targets](README.md#targets). The tools need the
rights listed under [Business accounts and stories](#business-accounts-and-stories). A change of the rights arrives
as a business connection update; `telegram.business.get` shows the current rights.

## Rights per tool group

Find your tool group, then give the bot the right. This table follows Telegram's Bot API documentation as far as it
documents each right; where Telegram names only "the appropriate administrator rights", the table gives the right
that fits the action and says so.

- **Administrator right** means the right of the bot as administrator of that chat, for example `can_delete_messages`.
- **Business bot right** means a right the business account owner grants.
- Tools offered only when a connection's `tools` list names them (for example bans or deletes) need the same
  rights as their group.

### Sending and editing

| Tools | Right of the bot |
| --- | --- |
| Send, forward, or copy text, media, albums, rich messages, polls, locations, venues, contacts, dice, stickers, games | In a channel: administrator right `can_post_messages`. In a group, the member permissions of the chat can forbid it. |
| Edit or delete the bot's own messages | In a channel: `can_post_messages`. Otherwise none; a message older than 48 hours cannot be deleted. |
| Delete messages of others | In a group: administrator. In a supergroup or channel: `can_delete_messages`. In a channel's direct messages chat: `can_manage_direct_messages`. In a private chat the bot also deletes incoming messages. |
| Edit messages of others in a channel | `can_edit_messages` |
| Reactions: remove one or all | In a group or supergroup: `can_delete_messages`. |

### Pins

| Tools | Right of the bot |
| --- | --- |
| Pin, unpin, unpin all | In a group: `can_pin_messages`. In a channel: `can_edit_messages`. In a private chat or a channel's direct messages chat: none. |
| Unpin all of a topic | `can_pin_messages` in the supergroup. |

### Members, join requests, and permissions

| Tools | Right of the bot |
| --- | --- |
| Ban, unban, restrict, ban or unban a sender chat | Administrator with `can_restrict_members`, as far as Telegram documents it ("the appropriate administrator rights"). Restricting works in supergroups only. |
| Set default member permissions of a chat | `can_restrict_members` |
| Promote or demote | `can_promote_members`; the bot can give only a subset of its own rights. |
| Set an administrator title | Only for an administrator the bot promoted. |
| Set a member tag | `can_manage_tags` |
| Decline a join request | `can_invite_users` (also needed to receive join request updates) |

### Chats, invite links, and boosts

| Tools | Right of the bot |
| --- | --- |
| Read chat, member count, administrators | None documented. |
| Read one member, and the member checks of the user tools | Telegram guarantees the answer for other users only if the bot is an administrator. |
| Change title, description, photo, group sticker set | Administrator with `can_change_info`, as far as Telegram documents it ("the appropriate administrator rights"). |
| Read the primary invite link | None documented. |
| Revoke an invite link | Administrator; `can_invite_users` fits, as far as Telegram documents it. The link must be one the bot created. |
| Read chat boosts of a user | Administrator rights in the chat |
| Leave a chat | None |

### Topics

| Tools | Right of the bot |
| --- | --- |
| Create a topic; edit, close, reopen a topic; hide, show, rename, close the General topic | `can_manage_topics`. Editing, closing, and reopening need no right for a topic the bot created. |
| Delete a topic | `can_delete_messages` |
| Unpin all of a topic | `can_pin_messages` |

### Business accounts and stories

| Tools | Business bot right |
| --- | --- |
| Reply (send, edit, checklists) | `can_reply`: private chats with incoming messages in the last 24 hours. Telegram names no separate right for checklists. |
| Mark as read | `can_read_messages` |
| Delete messages | `can_delete_sent_messages` for the bot's own, `can_delete_all_messages` for any |
| Set or remove the profile photo | `can_edit_profile_photo` |
| Change the username | `can_edit_username` |
| Read gifts and the Stars balance | `can_view_gifts_and_stars` |
| Post, edit, delete, repost stories | `can_manage_stories`; a repost needs it on both accounts. |

Telegram's method descriptions call the name, username, and bio rights `can_change_name`, `can_change_username`,
and `can_change_bio`; its `BusinessBotRights` object calls them `can_edit_name`, `can_edit_username`, and
`can_edit_bio`. The grant in Telegram's business settings decides.

### Bot-wide tools

These need the `bot` target and no right in any chat, only the token: the bot identity, webhook status, commands,
menu button, profile, default administrator rights, Stars balance and transactions, available gifts and gifts of a
user, sticker sets of the bot, files, answers to callback, inline, and join request queries, and
`telegram.updates.confirm`. Inline queries also need `/setinline` in BotFather; see [Create a bot](#create-a-bot).
Reading updates needs rights only for some update types: chat member updates and boost updates need the bot as
administrator, and join request updates need `can_invite_users`.

## What happens without a right

Qatlas does not read the rights of the bot beforehand. The tool is sent, and Telegram refuses it. The error says
`permission`, `provider-error`, or `not-found`; see [Fixing errors](#fixing-errors). No field is left out for a
missing right. A tool that checks membership first (the user tools and `telegram.members.promote`) is refused by
Qatlas before the change if the user is not a member or the status is unclear.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. The message names the operation and what to check.

### Reading a refusal

```text
permission: Telegram refused this operation; check the rights of the bot in Telegram
```

- Qatlas' own message is all there is. **No provider answer follows**: Telegram's error text is never passed on, so
  the page cannot tell from the message which right is missing. Find the tool in
  [Rights per tool group](#rights-per-tool-group) and compare the bot's rights in the chat.
- A change may have taken effect although the call reports an error after a lost connection or a server error. The
  message then ends with "the change may have taken effect, check the chat before repeating it".

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Telegram rejected the bot token | The token is wrong, revoked, or from another bot, or the service address is another one | Copy the token again from @BotFather (`/token` generates a new one) and store it with `qatlas credential set <credential> bot-token`. |
| `auth`: the bot token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$TELEGRAM_BOT_TOKEN" \| qatlas credential set <credential> bot-token`. |
| `permission`: Telegram refused this operation; check the rights of the bot in Telegram | The bot lacks the right in that chat or is not a member of it | Find the tool in [Rights per tool group](#rights-per-tool-group) and give the right, for example `can_delete_messages` to delete or `can_pin_messages` to pin. Add the bot to the chat if it is not a member. |
| `provider-error`: Telegram rejected the operation (HTTP 400) | Telegram refuses the request: a wrong chat or message, an invalid value, or a missing right. Telegram's reason is not shown | Check the chat and the message first, then the right of the tool group in [Rights per tool group](#rights-per-tool-group). |
| `provider-error`: Telegram rejected the operation (HTTP 404) | Telegram rejects the request; its reason is not shown | Check the token with the connection test and the chat ID of the target. |
| `provider-error`: Telegram reported a conflict with another request or webhook of this bot | A webhook is active, or another program reads the updates of the same bot | Stop the other consumer or remove the webhook outside Qatlas; see [Updates](updates.md). `telegram.webhook.get` shows whether a webhook is active. |
| `provider-error`: the Telegram chat was migrated to a supergroup; adjust the connection's target | The group became a supergroup and has a new ID | Change the target to the new chat ID; see [Targets](README.md#targets). |
| `rate-limited`: Telegram rate-limited the operation | Too many requests | Wait and retry. |
| `provider-error`: the chat is not bound to this connection (also `from_chat`) | The chat is outside the connection's targets | Not a bot problem. Pass a bound chat or add the target; see [Chat argument](README.md#chat-argument). |
| `provider-error`: membership could not be verified | The user is no current member of the chat, or the bot cannot see the membership | Check the user ID; give the bot administrator rights in the chat. |
| `unreachable`, `timeout`, `tls` | Telegram or the service address cannot be reached | Check the network and the base URL; see [Connection](README.md#connection). |
| Updates arrive empty in a group | Privacy mode hides the messages | See [Privacy mode](#privacy-mode). |
