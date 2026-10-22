---
description: >
  Describes the Telegram tools that ban, unban, restrict, and promote members, ban sender chats, and decline join requests.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Members

## Banning

`telegram.members.ban` (`delete`) bans one user and requires a `tools` list; `until_date` limits the ban, and
`revoke_messages` also deletes the user's messages. `telegram.members.unban` (`update`) always sends
`only_if_banned`, so a current member is never removed and nobody is invited. `telegram.sender-chats.ban`
(`delete`, `tools` list) and `telegram.sender-chats.unban` (`update`) act on a channel as sender; it is only the
object of the call, never the target.

## Restricting and promoting

`telegram.members.restrict` (`update`) requires a `tools` list and sets the complete `permissions` object of a user
in a supergroup: every ChatPermissions boolean is required and sent explicitly, a missing or unknown field is
refused before any request, and permissions are independent (`use_independent_chat_permissions`).

`telegram.members.promote` (`update`) requires a `tools` list and sets the complete `rights` object of an
existing member the way `restrict` sets permissions; all `false` demotes. Membership is checked with
`getChatMember` first, and a non-member or an unclear status is refused before any change. `can_promote_members`
and `can_invite_users` widen who may add administrators and members; the bot's default administrator rights are
not offered. `telegram.members.set-admin-title` (an administrator the bot promoted) and `telegram.members.set-tag`
(a regular member) set a value of up to 16 characters without emoji, checked locally and conservatively; an
empty value removes it.

## Join requests

`telegram.join-requests.decline` (`update`) declines the pending join request of `user_id` for a bound chat; joining
is never approved by any tool.
