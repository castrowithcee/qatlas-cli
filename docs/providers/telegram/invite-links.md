---
description: >
  Describes the Telegram tools for reading the primary invite link and revoking invite links.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Invite links

`telegram.invite-links.primary` (`read`) returns the primary invite link of the chat;
[`telegram.chats.get`](chats.md#reading-chats) never shows it. An invite link grants joining the chat without
sign-in; `telegram.invite-links.primary` is therefore available only through a `tools` list that names it, and no
other tool returns an invite link. The link carries the data class `telegram-invite-link`: it appears only in this
result, never in errors, logs, or the audit trail. A value that is not a `t.me` invite link is dropped.

`telegram.invite-links.revoke` revokes one link of the bound chat and requires a `tools` list. It accepts only
`https://t.me/+TOKEN` or `https://t.me/joinchat/TOKEN` with the exact host `t.me`, checked before the
credential is read. The request carries only the bound chat and the link, so Telegram refuses links of other
chats. The result is only `revoked: true`, never the link. Revoking the primary link makes Telegram generate a
new one that the result does not contain; creating, exporting, or editing invite links is not offered.
