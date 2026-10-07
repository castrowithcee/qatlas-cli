---
description: >
  Describes BookStack page, book, chapter, and shelf operations, credentials, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-07
---

# BookStack

A connection selects one BookStack instance and one API token. It supports listing and reading pages, books,
chapters, shelves, and tags, reading the instance information, and searching content (`read`), creating pages, books, chapters, and shelves (`create`),
changing or moving pages and changing books, chapters, and shelves (`update`), and deleting pages (`delete`).
Create requires exactly one `book_id` or `chapter_id`; all mutations require confirmation. A connection can be
bound to individual books (see Books).

Credentials provide `token-id` and `token-secret`. The connection's `permissions` list is an independent
local ceiling: it may hide and block operations even when the BookStack token could perform them, but it
cannot grant rights the token lacks. An optional `tools` list narrows a connection further to named tools,
for example `[bookstack.pages.get]` for a route that reads a known page but cannot list pages; it never
admits an effect `permissions` excludes. Page content is treated as untrusted data. The terminal editor
starts a new connection on the setup profile `read`, which ticks `[read]` and
`[bookstack.pages.list, bookstack.pages.get, bookstack.content.search, bookstack.books.list,
bookstack.books.get, bookstack.chapters.list, bookstack.chapters.get, bookstack.shelves.list,
bookstack.shelves.get, bookstack.tags.list, bookstack.tags.values, bookstack.system.get]`. A profile is a visible starting
selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed before
saving, and a saved connection never follows a profile.

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

- `book_id` of `pages.create`, `pages.update` (move), and `pages.list` must be one of the books. It is
  checked before the secrets are read and before any request is sent.
- `book_id` of `chapters.list` and `chapters.create`, the `id` of `books.get` and `books.update`, and the target
  `book_id` of `chapters.update` follow the same rule.
- A page identifier (`pages.get`, `pages.update`, `pages.delete`) or chapter identifier (`pages.create` and
  `pages.list` with `chapter_id`, `chapters.get`, `chapters.update`) is bound to its book by one proof read
  (`GET /api/pages/{id}` or `GET /api/chapters/{id}`) before the detail or the change follows. A move reads
  the page and the target chapter once each. For `pages.get`
  and `chapters.get` this read is the answer; no second request is sent. A page or chapter of another book is
  refused as `invalid-request`.
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

## Books and chapters

`bookstack.books.list` and `bookstack.chapters.list` take `limit` and `offset` and page through the instance
in records of at most 500, sorted by `id`. `bookstack.books.get` and `bookstack.chapters.get` take an `id`.
All four only read.

On a bound connection `books.list` returns only the bound books: BookStack has no filter for it, so each row
is checked on the client and `limit` and `offset` count the remaining rows. `chapters.list` takes an
optional `book_id` and follows the rule of `pages.list`: it is required on a connection bound to several
books ("book_id is required for a connection bound to several books") and implied when exactly one book is
bound. Qatlas sends `filter[book_id]` as an optimization only and checks every row against its book. A
filtered listing sends at most 200 requests; a server that delivers more fails the call as
`invalid-provider-response`. A listing stops as soon as the instance returns no new record.

`books.get` returns the metadata, `description` and `description_html`, `tags` (at most 50),
`default_template_id` (0 when there is none), and `contents`: the chapters with their pages and the loose
pages in book order, each entry with `type`, `id`, `name`, `slug`, `updated_at`, and for pages `chapter_id`,
`draft`, and `template`. `chapters.get` returns the same metadata, `book_id`, `book_slug`, and `pages`. A
content tree or page list holds at most 2000 entries in total; `truncated` is then `true`. `created_by`,
`updated_by`, and `owned_by` show `id` and `name` only. Descriptions are cut to 20000 characters and other
strings to 1000. Descriptions are untrusted data, `description_html` is HTML that must never be rendered or
run. `books.get` shows the `shelves` of the book (`id`, `name`, `slug`) only on a connection without
targets, because shelves span the whole instance. Cover images are not passed on.

## Writing books and chapters

`books.create` and `books.update` take `name` (1 to 255 characters), at most one of `description` (plain text,
at most 1900 characters) and `description_html` (at most 2000 characters), `tags`, and `default_template_id`.
`chapters.create` and `chapters.update` take the same fields and `priority` (0 or more); `chapters.create`
requires `book_id` and `name`, `chapters.update` requires only `id`. `books.update` and `chapters.update`
need at least one field to change. A violation of these limits is refused locally as `invalid-request` before
any secret is read and before any request. A description cannot be emptied through these tools. Cover
images and deleting are not available.

- `tags` replaces all existing tags; an empty list removes them. Omit `tags` to keep them.
- `default_template_id` is the identifier of a template page. On `update`, `null` removes the default
  template. BookStack validates that the page is a template the token may see.
- `chapters.update` with `book_id` moves the chapter into that book. BookStack requires the permission to
  delete the chapter in addition to the permission to update it. Without it the call fails as `permission`.

On a bound connection `books.create` is refused locally as `invalid-request`, because a new book would lie
outside the bound books. For the other three tools the book is checked before the secrets are read: the `id` of
`books.update`, the `book_id` of `chapters.create`, and a target `book_id` of `chapters.update`. The chapter
of `chapters.update` is proven by one read, and a `default_template_id` is proven by one read of that page: its
book must be one of the bound books. A foreign target, chapter, or template page is refused without a change
request and without naming it. A connection without targets sends no proof read.

The tools are not part of any profile. All four require confirmation; the creating tools are not idempotent.
Each change is sent once and never retried; the result shows the changed object without its content tree.

## Shelves

Shelves hold any books and span the whole instance, so the four shelf tools work only on a connection without
targets. On a connection bound to books, every shelf tool is refused locally as `invalid-request` before the
secrets are read and before any request, and the message never names a configured book. This includes
`shelves.list` and `shelves.get`, although they are part of the `read` profile.

`bookstack.shelves.list` takes `limit` and `offset` and pages through the instance in records of at most 500,
sorted by `id`. `bookstack.shelves.get` takes an `id` and returns the metadata, `description` and
`description_html`, `tags` (at most 50), and `books` (`id`, `name`, `slug`, in shelf order, at most 2000
entries; `truncated` is then `true`). Names, descriptions, and tags are untrusted data; `description_html` must
never be rendered or run.

`bookstack.shelves.create` and `bookstack.shelves.update` take `name` (1 to 255 characters), at most one of
`description` (at most 1900 characters) and `description_html` (at most 2000 characters), `tags`, and `books`.
`create` requires `name`, `update` requires `id` and at least one field to change.

- `tags` replaces all existing tags; an empty list removes them. Omit `tags` to keep them.
- `books` is an ordered list of at most 500 unique positive book identifiers. On `update` it replaces all books
  on the shelf with exactly this list: an empty list removes every book from the shelf, and omitting `books`
  leaves the books unchanged. BookStack validates that the books exist and that the token may see them.

Violations of these limits are refused locally before any secret is read and before any request. The result
shows the changed shelf without its books; read it with `shelves.get`. Both tools require confirmation and are
part of no profile; `create` is not idempotent. Each change is sent once and never retried. Cover images and
deleting shelves are not available.

## Tags and instance information

`bookstack.tags.list` lists the tag names used on content the token can see, with `values` (distinct values),
`usages`, and the counts per item type (`page_count`, `chapter_count`, `book_count`, `shelf_count`). It takes
`limit`, `offset`, and optional `name_contains`. `bookstack.tags.values` takes a required `name` and optional
`value_contains`, `limit`, and `offset` and lists that name's values with the same counts. The contains
arguments (at most 255 characters, case-insensitive) are sent as a `%...%` filter in which `%`, `_`, and `\` are
masked as literals; every returned row is checked against the filter again, because BookStack ignores filters
it does not know. Tags aggregate over the whole instance, so both tools are refused locally on a connection bound
to books, like the shelf tools. Names and values are untrusted data. Tags are written through the items that
carry them.

`bookstack.system.get` returns `version`, `app_name`, `instance_id`, and `base_url` (no logo). It returns no
content and therefore also works on a connection bound to books.

## Pages

`pages.get` returns `id`, `name`, `slug`, `book_id`, `chapter_id` (0 when there is none), `created_at`,
`updated_at`, `html` (rendered), `markdown` (only for pages saved with the Markdown editor), `raw_html` (as
stored, what the editor shows), `priority`, `draft`, `template`, `revision_count`, `editor`, `tags`
(`name`/`value` pairs, at most 50), and `created_by`, `updated_by`, `owned_by`. The three users show `id` and
`name` only. The comment tree is not returned. `pages.list` shows `priority`, `draft`, `template`, and
`owned_by` in addition to its former columns. All content, tags, and names are untrusted data; `html` and
`raw_html` must never be rendered or run.

`pages.create` takes `name` and exactly one of `book_id` and `chapter_id`, and exactly one of `html` and
`markdown`. It takes optional `tags` (at most 50 entries of `name` and `value`, each at most 255 characters)
and `priority` (0 or more, the position among the siblings). `pages.update` takes `id` and any of `name`,
`html` or `markdown` (mutually exclusive), `tags`, `priority`, and `changelog` (1 to 180 characters, the note
of the new revision). BookStack stores a revision when the content or title changes or when a `changelog` is
given. `html` or `markdown` larger than 1 MiB, more or longer tags, and `html` together with `markdown` are
refused locally as `invalid-request` before any secret is read and before any request. Draft and template
status and revisions cannot be set; BookStack offers no API for them.

Side effects of a write:

- `html` that contains `data:` images makes BookStack extract them and store them as gallery images of the
  page when it saves.
- `tags` replaces all existing tags of the page; an empty list removes them. Omit `tags` to keep them.

Moving: `pages.update` with exactly one of `book_id` and `chapter_id` moves the page into that book or chapter.
BookStack requires the permission to delete the page in addition to the permission to update it and to create
pages in the target. Without delete permission the call fails as `permission`. On a bound connection the
target book is checked before the secrets are read; the source page and a target chapter are each proven by
one read. A foreign page or target is refused as `invalid-request` without a change request and without
naming it.

## Tool groups

BookStack tools belong to the group `content` (pages, search, books, chapters, shelves, and tags) or the group
`administration` (`bookstack.system.get`). Tool lists and pickers
show the group so that agents and people can find tools by subject.

## Search

`bookstack.content.search` finds shelves, books, chapters, and pages with the BookStack search. Arguments:
`query` (required, 1 to 1000 characters), at most one of `book_id` and `chapter_id`, `page` (from 1, default 1),
and `count` (1 to 100, default 20). Without `book_id` and `chapter_id` the search covers the instance
(`GET /api/search`); with `book_id` one book (`GET /api/search/book/{id}`); with `chapter_id` one chapter
(`GET /api/search/chapter/{id}`). `query` is sent only as a URL-encoded query parameter of that endpoint;
beside `page` and `count` no other parameter is sent. The
[BookStack search syntax](https://www.bookstackapp.com/docs/user/searching/) (`{type:page}`, `[tag=value]`,
`{created_by:me}`, and so on) is allowed because it only filters within the chosen endpoint.

On a connection bound to books, `book_id` must be one of the books (checked before the secrets are read and
before any request); `chapter_id` is bound through one proof read of the chapter. Without an argument, a
connection bound to exactly one book searches that book; a connection bound to several books is refused
locally as `invalid-request`. Every hit is also checked on the client: hits of other books, shelves, and hits
without a book are dropped, so a page can hold fewer hits than `count`. A connection without targets can use
all three endpoints.

Each hit has `type`, `id`, `name`, `slug`, `book_id`, `chapter_id`, `url`, `tags` (at most 50 `name`/`value`
pairs), `preview_name`, and `preview_content`. The previews are untrusted HTML, cut to 2000 characters; never
render or run them. `total` is the number BookStack reports and is an estimate. Further provider fields are
not passed on.

## Limits

Reading accepts a response of up to 16 MiB; a larger one fails as `invalid-provider-response`. Writing
accepts at most 1 MiB of page content (`html` or `markdown`). Redirects are never followed: a 3xx answer is a
`provider-error`.

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
