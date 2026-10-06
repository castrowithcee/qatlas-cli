---
description: >
  Describes BookStack page operations, credentials, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-06
---

# BookStack

A connection selects one BookStack instance and one API token. It supports listing and reading pages
(`read`), creating pages (`create`), changing title or Markdown (`update`), and deleting pages (`delete`).
Create requires exactly one `book_id` or `chapter_id`; all mutations require confirmation. A connection can be
bound to individual books (see Books).

Credentials provide `token-id` and `token-secret`. The connection's `permissions` list is an independent
local ceiling: it may hide and block operations even when the BookStack token could perform them, but it
cannot grant rights the token lacks. An optional `tools` list narrows a connection further to named tools,
for example `[bookstack.pages.get]` for a route that reads a known page but cannot list pages; it never
admits an effect `permissions` excludes. Page content is treated as untrusted data. The terminal editor
starts a new connection on the setup profile `read`, which ticks `[read]` and
`[bookstack.pages.list, bookstack.pages.get]`. A profile is a visible starting selection, not a role: only the
ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved connection
never follows a profile.

## Credential and base URL

`base_url` must be an `https` URL of the BookStack instance, optionally below an installation path (for
example `https://host/wiki`). A URL with `http`, user info, a query (even an empty `?`), or a fragment is
refused before any secret is read and before any request, and the refusal does not repeat the URL. There is
no exception for `http`, not even for loopback, so the token is never sent in clear text. No redirect is
followed, so the token never travels to another host.

Existing connections with an `http://` URL are refused after this change and fail closed. Put TLS in front
of the instance, for example with a reverse proxy, and change `base_url` to the `https` address.

## Books

A connection may be bound to books with `target` or the `targets` list; each entry has the form
`book/BOOK_ID`, with a positive integer without a leading zero. A list holds at most 100 entries, names no
book twice, and accepts no wildcard. Without a target, everything the token reaches stays reachable.

Behavior change: a target that was set freely before and does not match this form now makes the connection
invalid. The connection fails closed, in the configuration check and at every call, and the message never
quotes the configured value.

On a bound connection every tool stays inside the listed books and a refusal never names the foreign target:

- `book_id` of `pages.create` and `pages.list` must be one of the books. It is checked before the secrets
  are read and before any request is sent.
- A page identifier (`pages.get`, `pages.update`, `pages.delete`) or chapter identifier (`pages.create` and
  `pages.list` with `chapter_id`) is bound to its book by one proof read (`GET /api/pages/{id}` or
  `GET /api/chapters/{id}`) before the detail or the change follows. For `pages.get` this read is the answer;
  no second request is sent. A page or chapter of another book is refused as `invalid-request`.
- A connection without targets sends no proof read.

`bookstack.pages.list` takes the optional, mutually exclusive arguments `book_id` and `chapter_id`. On a bound
connection `book_id` must be one of the books and `chapter_id` is bound through its chapter. Without an
argument, a connection bound to exactly one book lists that book; a connection bound to several books is
refused locally as `invalid-request`: "book_id is required for a connection bound to several books". Qatlas
sends `filter[book_id]` and `filter[chapter_id]` as an optimization only. BookStack silently ignores a filter
it does not know, so each row is checked against its book (and chapter) on the client and a foreign row is
dropped. `limit` and `offset` then count the remaining rows. A filtered listing sends at most 200 requests; a
server that delivers more fails the call as `invalid-provider-response`.

Limit of the binding: the proof read and the change are two requests. A page moved to another book between
them, for example by another user, can still be changed once.

## Limits

Reading accepts a response of up to 16 MiB; a larger one fails as `invalid-provider-response`. Writing
accepts at most 1 MiB of page content. Redirects are never followed: a 3xx answer is a `provider-error`.

## Deleting

`bookstack.pages.delete` moves the page to the BookStack recycle bin; it does not erase it permanently.
Behavior change: the tool requires a tool allow list and is part of no profile. A connection offers it only
when its `tools` list names `bookstack.pages.delete` and its `permissions` include `delete`; a connection
without a `tools` list no longer offers it.

## Errors

Errors carry a fixed class and never the text BookStack sent.

| Status | Class |
| --- | --- |
| 401 | `auth` |
| 403 | `permission`: the token's user lacks the BookStack role permission for this action, or the token expired or lacks API access (the connection test reports 403 as `auth`) |
| 404 | `not-found` |
| 429 | `rate-limited` |
| 422 | `provider-error`, naming at most the invalid argument names, never values |
| other | `provider-error (HTTP n)` |

A create, update, or delete that ends in a timeout, a cancellation, a 5xx answer, or an unreadable answer
adds: "this change may have taken effect, read the current state in BookStack before repeating it". Each
change is sent exactly once and never retried.
