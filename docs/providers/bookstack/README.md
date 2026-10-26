---
description: >
  Describes the BookStack provider: connection, credential, base URL, targets, tool groups, limits, deletion rules, and error classes.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-26
---

# BookStack

A connection selects one BookStack instance and one API token. It supports reading (`read`), creating
(`create`), changing or moving (`update`), and deleting (`delete`) BookStack content. All mutations require
confirmation. A connection can be bound to shelves, books, chapters, or pages (see [Targets](#targets)). The
tools are sorted into groups (see [Tool groups](#tool-groups)). [Set up BookStack](setup.md) leads through
creating an API token and lists the permissions each tool group needs.

Credentials provide `token-id` and `token-secret`. A connection selects its tools by `effects`, which offers
every tool of the listed effects, or by a `tools` list, which offers exactly the named tools, for example
`[bookstack.pages.get]` for a route that reads a known page but cannot list pages; it never holds both. Either
way the selection is an independent local ceiling: it may hide and block operations even when the BookStack
token could perform them, but it cannot grant rights the token lacks. Page content is treated as untrusted
data. The terminal editor starts a new connection on the setup profile `read`. A profile is a visible starting
selection, not a role: only the ticked `tools` are saved, every tick can be changed before saving, and a
saved connection never follows a profile.

## Credential and base URL

`base_url` must be an `https` URL of the BookStack instance, optionally below an installation path (for
example `https://host/wiki`). A URL with `http`, user info, a query (even an empty `?`), or a fragment is
refused before any secret is read and before any request, and the refusal does not repeat the URL. There is
no exception for `http`, not even for loopback, so the token is never sent in clear text. No redirect is
followed, so the token never travels to another host.

Put TLS in front of the instance, for example with a reverse proxy, and set `base_url` to the
`https` address.

## Targets

A connection may be bound with `target` or the `targets` list: at most 100 entries, none twice, no wildcard,
each identifier a positive integer without a leading zero. A target that does not match these forms makes the
connection invalid; it fails closed in the configuration check and at every call, and the message never quotes
the value. Without a target, everything the token reaches stays reachable. Several targets reach their union.

| Target | Reaches | Proof |
| --- | --- | --- |
| `shelf/SHELF_ID` | the books on the shelf at the time of the call, each like a bound book, and the shelf itself through `shelves.get` | one read of the shelf per call, only when a book is not bound itself; a book taken off the shelf is out of reach with the next call, and a shelf that cannot be read fails the call |
| `book/BOOK_ID` | the book with its chapters and pages | a `book_id` locally, a page or chapter by one read of it |
| `chapter/CHAPTER_ID` | the pages of the chapter, also new ones, with their attachments, images, and comments; the chapter itself only to read, search, and export | a page by one read of it (its `chapter_id`); the chapter itself needs none |
| `page/PAGE_ID` | the page with its attachments, images, and comments | none for the page itself |

Every identifier from an argument or an answer (page, chapter, book, attachment, image, comment, template page,
search hit) is proven against the targets before anything is read, changed, or returned. An attachment, image,
or comment is proven through its page, a move through its source and its target. A refusal is an
`invalid-request` that names neither the object nor a target. Filters sent to BookStack only narrow a list:
BookStack silently ignores a filter it does not know, so every row and hit is checked on the client against its
own book, chapter, and page, and `limit` and `offset` count the remaining rows. A filtered listing sends at most
200 requests; a server that delivers more fails the call as `invalid-provider-response`. A connection without
targets sends no proof read.

Without `book_id` and `chapter_id`, `pages.list` and `content.search` use the only bound book or the only bound
chapter, and `chapters.list` the only bound book. A connection bound to pages alone lists these pages; its search
reads the instance search in pages of 100 hits until every bound page is found, at most 20 requests, and `total`
counts the bound pages found. Every other binding needs an argument and is refused locally otherwise.

A tool that a binding does not reach is not offered, and a call is refused locally before any secret is read:

| Binding | Not reachable |
| --- | --- |
| any target | the instance-wide tools: `books.create`, the shelf tools except `shelves.get`, content permissions of a `bookshelf`, `tags.list`, `tags.values`, and the recycle bin, user, role, audit log, and import tools |
| no `shelf/` | `shelves.get` |
| no `book/` or `shelf/` | the whole-book tools `books.list`, `books.get`, `books.update`, `books.delete`, `books.set-cover`, `chapters.list`, `chapters.create`, `chapters.update`, and `chapters.delete`, the export of a `book`, and content permissions of a `book` or `chapter` |
| only `page/` | also `chapters.get`, `pages.create`, and the export of a `chapter` |

Limit of the binding: the proof read and the action are two requests. An object moved out of the targets
between them, for example by another user, can still be changed once.

### Choosing targets

The picker lists what the token reaches, also on a draft, and changes nothing. The top level holds the shelves and
all books, also those on no shelf. A shelf opens its books, a book its chapters and the pages outside any chapter,
a chapter its pages; draft pages are not offered.

A URL of the service's own origin (installation path allowed) becomes a target: `/shelves/SLUG`, `/books/SLUG`,
`/books/SLUG/chapter/SLUG`, and `/books/SLUG/page/SLUG`. The slug is resolved by a list read, a chapter or page slug
within its book. An unknown or ambiguous slug, another origin, or another form gives nothing.

## Tool groups

Tool lists and pickers show the group so that agents and people can find tools by subject.

- `content`: pages, search, export, books, chapters, shelves, and tags, see [content](content.md).
- `comments`: page comments, see [comments](comments.md).
- `files`: page attachments and images, see [files](files.md).
- `administration`: instance information, content permissions, the recycle bin, users, roles, and the audit log,
  see [administration](administration.md).
- `imports`: ZIP imports, see [imports](imports.md).

## Sorting and filtering lists

List tools that take `sort` and `filters` pass them to BookStack as an optimization only. BookStack silently ignores a filter it does
not know, so Qatlas checks every row against all filters and against the targets; `limit` and `offset` count the remaining rows. A filter never
widens the target binding: when a filter uses the same key as the binding, the binding value is the one sent, and all filters are still checked.

Values are checked before any request. A plain date stands for midnight UTC, and a row without the filtered value is dropped.
Text filters are checked again in Qatlas without case distinction; a row that BookStack matches only through its database collation, for example accent-insensitive matching, is dropped.
With a sort other than `id`, a listing that needs more than one request can skip rows or meet a row twice when rows share the sort value or change during the listing; a repeated row is dropped.

## Limits

Reading accepts a response of up to 16 MiB (an inline export 8 MiB, a download 512 MiB; an attachment answer 96 MiB of content, an attachment download 72 MiB, an image download 64 MiB); a larger one fails as `invalid-provider-response`. Writing
accepts at most 1 MiB of page content (`html` or `markdown`) and an attachment or image upload of at most 50 MiB. Redirects are never followed: a 3xx answer is a
`provider-error`.

## Deleting

Deleted pages, books, and chapters move to the BookStack recycle bin. Only a BookStack admin can restore them, until
the recycle bin is emptied automatically (30 days by default, `RECYCLE_BIN_LIFETIME`; with `0` the deletion is
immediate and final). The rules per object are on the page of its tool group. Each call sends exactly one `DELETE`
without retry; after a timeout or a 5xx answer the result is reported as uncertain.

Every delete tool (pages, books, chapters, shelves, comments, attachments, images) requires a tool allow list and is part of no profile. A connection offers one only when its `tools`
list names it; a connection that selects by `effects` does not offer them.
On a connection with targets, deleting the object a target names is allowed where the binding reaches it (a
chapter needs a book or shelf target), but the target then points at nothing.

## Errors

Errors carry a fixed class and never the text BookStack sent.

| Status | Class |
| --- | --- |
| 401 | `auth` |
| 403 | `permission` (the connection test reports 403 as `auth`); see [Fixing errors](setup.md#fixing-errors) |
| 404 | `not-found` |
| 429 | `rate-limited` |
| 422 | `provider-error`, naming at most the invalid argument names, never values |
| other | `provider-error (HTTP n)` |

A create, update, or delete that ends in a timeout, a cancellation, a 5xx answer, or an unreadable answer
adds: "this change may have taken effect, read the current state in BookStack before repeating it". Each
change is sent exactly once and never retried.
