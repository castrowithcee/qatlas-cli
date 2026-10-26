---
description: >
  Describes Nextcloud Talk tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Nextcloud Talk

Group `talk`: conversations, participants, messages, polls, and file shares.

The tool group `talk` uses the OCS API of Talk. Only reads are in the setup profile `talk-read`; deleting and
closing a poll for good need a tools list. A `talk` target binds every conversation of the identity, `talk/TOKEN`
one; an unbound or malformed token is refused locally, before credential access or any request, unnamed. Texts,
names, questions, and addresses are untrusted data; addresses are never fetched. A rich object member that holds a
token, or a link to a public share or to a conversation by token, is an access value and is never reported.
Messages are read without moving the read marker, marking notifications read, waiting for new messages, or changing
the user status. Paging uses `cursor`; `lastKnownMessageId`, `includeLastKnown`, and `lastCommonReadId` are not
offered separately. The SIP dial-in PIN grants access to a call without login and is never reported. The token of a
federated conversation on its home server is an access value and is never reported; the home server address is.
Session IDs are access values and are never reported. The SIP call identifier is not documented as harmless and is
treated as an access value. Status fields appear only with `include_status`.

Only internal conversations and existing users and groups are managed: no public, guest, e-mail, phone, federated,
or team entries. `talk.rooms.create` needs the `talk` target; the role `talk.rooms.update` and `.delete` need is
under [Talk](setup.md#talk). Only `talk.rooms.mark-read` moves the read marker. `talk.participants.remove` and
`.moderator` take an `attendee_id` of `talk.participants.list`, spare owner and self, and need a tools list.
`talk.polls.vote` takes option IDs of `talk.polls.get`. `talk.files.share` needs `folder`, `talk`, a tools list and
shares read-only into private conversations; `shares.shares.delete` revokes. Every call first reads the `spreed`
capability or the conversation or poll it needs and refuses a missing feature (the app: [Talk](setup.md#talk)); a
change needs `confirm` and then sends one request. Check the list before repeating an unclear outcome (`send`:
`reference_id`; `talk.files.share`: `shares.shares.list`). 429 is rate-limited.
