---
description: >
  Describes the Telegram tools that read profile media, boosts, and personal chat messages of chat members.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-23
---

# Users

`telegram.users.profile-photos`, `.profile-audios`, `.chat-boosts`, and `.personal-chat-messages` read profile and
boost data of a `user_id`. Each call first checks with `getChatMember` that the user is a current
member of the bound chat and refuses a non-member before the actual request. Profile files appear only as a
`file_ref` bound to that chat. `.chat-boosts` needs administrator rights of the bot. Membership is checked on issue
only: a `file_ref` stays redeemable through `telegram.files.get` and `.download` (see [files.md](files.md)) after
the user leaves. `getUserChatBoosts` has no page size, so `.chat-boosts` prints a fixed maximum and marks more
as `truncated`. `.personal-chat-messages` stays at 20 messages, the most the API allows.
