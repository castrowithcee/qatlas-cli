---
description: >
  Describes BookStack page comment tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# BookStack comments

Group `comments`. The token rights are listed under [Comments](setup.md#comments).

Comments are read and managed on pages only; comments on other object types are not reachable.

`comments.list` takes an optional `page_id` (required on a connection with targets), `limit`, and `offset`. It
lists the comments of the page without their text. `parent_id` is the `local_id` of the parent comment on the
same page (0 for a top-level comment); `local_id` is a number scoped to the page, `id` is global. Without
`page_id` on a connection without targets, the page comments of the whole instance are listed. The filters sent
to BookStack are only an optimization: every row is checked again for the page and for the comment type, because
BookStack ignores filters it does not know, and `limit` and `offset` count the remaining rows.

`comments.get` takes the global `id` and returns the comment with `html` (untrusted data, never render or run
it), `archived`, and its direct replies, which are checked to belong to the same page and parent. The text of
each comment is cut at 64 KiB and at most 200 replies are returned; `truncated` says whether anything was cut.

`comments.create` takes `page_id`, `html` (1 to 65536 characters), an optional `reply_to` (the `local_id` of a
comment of the same page, not its global `id`), and an optional `content_ref` (at most 255 characters, the part
of the page content the comment is attached to). `comments.update` takes `id` and `html` and/or `archived`;
BookStack accepts `archived` for top-level comments only and refuses it for a reply. Create and update answer
with the comment fields without `html`; read the text with `comments.get`. Invalid input is refused locally as
`invalid-request` before any secret is read and before any request.

`comments.delete` takes `id` and deletes the comment for good: BookStack has no recycle bin for comments and
cannot restore them. It requires a tool allow list entry and is part of no profile, like the other delete
tools. `comments.create` is not idempotent; `comments.update` is idempotent. All three changes require
confirmation, send exactly one request, and are never retried; after a timeout or a 5xx answer the result is
reported as uncertain.

A comment that is not a page comment is refused. On a connection with targets, `get`, `update`, and `delete`
read the comment and prove its page before it is returned or changed (see [Targets](README.md#targets)).
