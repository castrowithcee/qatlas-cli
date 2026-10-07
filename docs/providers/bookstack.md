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
chapters, shelves, tags, page comments, page attachments, and gallery images, reading the instance information, and searching content (`read`), creating pages, books, chapters, shelves, comments, attachments, and images (`create`),
changing or moving pages and attachments and changing books, chapters, shelves, comments, and images (`update`), and deleting pages, books, chapters, shelves, comments, attachments, and images (`delete`).
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
bookstack.shelves.get, bookstack.tags.list, bookstack.tags.values, bookstack.comments.list, bookstack.comments.get,
bookstack.attachments.list, bookstack.attachments.get, bookstack.images.list, bookstack.images.get, bookstack.system.get, bookstack.content.export]`. A profile is a visible starting
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
any secret is read and before any request. A description cannot be emptied through these tools. Deleting is
not available; for cover images see below.

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
part of no profile; `create` is not idempotent. Each change is sent once and never retried. Deleting shelves is
not available.

### Cover images

`bookstack.books.setcover` and `bookstack.shelves.setcover` take `id` and `local_path` and set the cover image
from a local file (png, jpg, jpeg, gif, or webp, at most 50 MiB, streamed from disk from a directory the
connection releases for reading). The file type is checked before any secret is read or any file is opened. The
request is a multipart `POST` with `_method=PUT` and the file in the field `image`. `books.update` and
`shelves.update` take `remove_cover: true` to remove the cover (sent as `image: null`); it can be combined with
the other fields and is a valid change on its own, and `false` sends nothing. On a connection bound to books the
book `id` must be a bound book; the shelf variants are refused on such a connection. All of these require
confirmation, are not part of any profile, send one request without retry, and `setcover` is not idempotent.

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

## Content permissions

`bookstack.contentpermissions.get` reads the permission overrides of one `page`, `chapter`, `book`, or
`bookshelf` (`type` and `id`): the owner (`id`, `name`, `slug`), the role overrides (`role_id`, `display_name`,
`view`, `create`, `update`, `delete`; at most 100, otherwise `truncated`), and the fallback permissions
(`inheriting`, plus the four values when not inheriting) that apply to every role without an override. Owner and
role names are personal data and untrusted provider content.

`bookstack.contentpermissions.update` changes those permissions for existing roles in one request. Every
category is optional, but at least one is required, and BookStack applies exactly what is sent:

- `owner_id`: the new owner (a positive user id).
- `role_permissions`: replaces all role overrides. At most 100 entries with a unique positive `role_id` and all
  of `view`, `create`, `update`, `delete`. A category that is left out stays unchanged; an empty list removes
  all role overrides.
- `fallback_permissions`: `inheriting`, and all four values when `inheriting` is `false` (no values when it is
  `true`).

Public access is never opened. BookStack stores the fallback as an override that applies to every role without
an entry of its own, guests included, so Qatlas protects the guest role (the system role `public`). Before it
changes anything it determines that role (a read of the role list, at most 100 roles, and a read of the role to
confirm its system name and list its users), reads the current permissions of the item, and refuses locally,
without sending the change, when

- an entry for the guest role sets any value to `true`, unless the entry is identical to the guest entry that
  already exists,
- `role_permissions` is sent and omits an existing guest entry (also an empty list while a guest entry exists),
- `fallback_permissions` differs from the current fallback and the resulting role overrides (the sent
  `role_permissions`, otherwise the current ones) have no explicit guest entry with all four values `false`, or
- `owner_id` is a user of the guest role, because ownership would pass to anonymous visitors.

If the guest role cannot be determined (for example the token's user lacks the BookStack permission to manage
user roles, no role has the system name `public`, or the confirmation differs), the change is refused as well.
The refusals never name the other target or relay provider text. The guest lookup is made once per call and not
cached beyond it.

The tool requires confirmation and a tool allow list, is idempotent, is part of no profile, and sends exactly
one `PUT` without retry; after a timeout or a 5xx answer the result is reported as uncertain. On a connection
bound to books a `book` must be a bound book, a `chapter` or `page` is proven to lie in a bound book by one
read, and a `bookshelf` is only available on a connection without book targets.

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

## Recycle bin

`bookstack.recyclebin.list` lists the deleted items of the instance (`limit`, `offset`). Each entry carries `id`
(the deletion identifier), `deleted_by`, `created_at`, `deletable_type` (`page`, `chapter`, `book`, or
`bookshelf`), `deletable_id`, and of the deleted item only `name`, `slug`, `book_id`, `chapter_id`,
`parent_type`, `parent_id`, `pages_count`, and `chapters_count` where BookStack reports them. Names are untrusted
data.

`bookstack.recyclebin.restore` restores one deletion by `deletion_id` and returns `restore_count`.
`bookstack.recyclebin.destroy` destroys one deletion permanently and returns `delete_count`; this cannot be
undone. BookStack requires the role permissions `settings-manage` and `restrictions-manage-all` for all three.
The recycle bin spans the whole instance, so the tools are refused on a connection with book targets. Each
change sends exactly one request (`PUT` or `DELETE`) without retry; after a timeout or a 5xx answer the result is
reported as uncertain. The tools are part of no profile, and `destroy` additionally requires a tool allow list
that names it and the `delete` permission.

## Users, roles, and audit log

These tools cover the whole instance, so they are refused on a connection with book targets (before any secret is
read), are part of no profile, and are classified as `bookstack-people` data (e-mail addresses, external
authentication identifiers, IP addresses). Names and texts are untrusted data.

`bookstack.users.list` (`limit`, `offset`) and `bookstack.users.get` (`id`) need the role permission
`users-manage`. They return `id`, `name`, `slug`, `email`, `external_auth_id`, `created_at`, `updated_at`,
`last_activity_at`, `profile_url`, and `roles` (`id`, `display_name`). BookStack does not report roles in the
listing, so only `get` carries them. Avatar and edit addresses are never returned.

`bookstack.roles.list` (`limit`, `offset`) and `bookstack.roles.get` (`id`) need `user-roles-manage`. The listing
returns `id`, `display_name`, `description`, `system_name`, `external_auth_id`, `mfa_enforced`, `users_count`,
`permissions_count`, `created_at`, and `updated_at`. `get` returns the same descriptive fields plus the
`permissions` (names) and the `users` (`id`, `name`) of the role, at most 1000 users; `truncated` is true when the
users or permissions were cut.

### Changing roles

`bookstack.roles.create`, `bookstack.roles.update`, and `bookstack.roles.delete` need the role permission
`user-roles-manage`. Role permissions can escalate up to full administrative control of the instance, so these
tools require a tool allow list, are part of no profile, need confirmation, and are refused on a connection with
book targets. Each sends exactly one change request without retry; after a timeout, a 5xx answer, or an unreadable
answer the result is reported as uncertain. `create` and `update` return the role like `roles.get`.

- `roles.create` takes `display_name` (3 to 180 characters, required), `description` (at most 180 characters),
  `mfa_enforced`, and `permissions`.
- `roles.update` takes `id` and the same fields; at least one must be given and a field that is left out stays
  unchanged. `permissions` replaces the whole list, so an empty list removes every permission.
- `roles.delete` takes `id` and is final: the users of the role lose it, the content permissions of the role are
  removed, and the API offers no migration of the users to another role. BookStack refuses to delete the
  registration role; Qatlas also reads the role first and refuses every system role (a role with a system name,
  such as `admin` or `public`) without a change.

`permissions` accepts only names from a fixed list of the permissions that the role form of BookStack v26.09.1
offers: `access-api`, `content-export`, `content-import`, `editor-change`, `receive-notifications`,
`restrictions-manage-all`, `restrictions-manage-own`, `settings-manage`, `templates-manage`, `user-roles-manage`,
`users-manage`, `revision-view-all`, and the content permissions `book-`, `bookshelf-`, `chapter-`, and `page-`
(`view`, `update`, `delete`, and `create`, each with `-all` and `-own`, except that `book-` and `bookshelf-` have
no `create-own`), and `image-`, `attachment-`, and `comment-` (`create-all`, `update-all`, `update-own`,
`delete-all`, `delete-own`). Unknown or repeated names are refused before any secret is read or request is sent.

The guest role (system role `public`, public access) is never changed. `roles.update` reads the role and
determines the guest role (a read of the role list, at most 100 roles, and a read of that role) and refuses,
without a change, when the role is the guest role or when the guest role cannot be determined.

`external_auth_id` is not supported: Qatlas never binds a role to the groups of an external identity provider,
so the argument does not exist and no request body contains it.

`bookstack.auditlog.list` needs `settings-manage` and `users-manage`. It contains IP addresses and sign-in events
of all users, so it is offered only by a connection whose `tools` list names it. It takes `limit`, `offset`, and
the filters `type` (lowercase letters, digits, and underscores, at most 64 characters), `user_id`,
`loggable_type` (`page`, `chapter`, `book`, or `bookshelf`), `loggable_id`, and `created_after`/`created_before`
(`YYYY-MM-DD` or RFC 3339; dates are UTC and a date alone means the start of that day). Values are checked before
any request. BookStack ignores filters it does not know, so every returned entry is checked again against all
filters. Entries carry `id`, `type`, `detail`, `user_id`, `user_name`, `loggable_type`, `loggable_id`, `ip`, and
`created_at`.

## Tool groups

BookStack tools belong to the group `content` (pages, search, books, chapters, shelves, and tags), the group
`comments` (page comments), the group `files` (page attachments and images), or the group `administration` (`bookstack.system.get`, the content permission tools, the recycle bin tools, and the user, role, and audit log tools, including the role write tools). Tool lists and pickers
show the group so that agents and people can find tools by subject.

## Comments

Comments are read and managed on pages only; comments on other object types are not reachable.

`comments.list` takes an optional `page_id` (required on a connection bound to books), `limit`, and `offset`. It
lists the comments of the page without their text: `id`, `page_id`, `parent_id`, `local_id`, `content_ref`,
`created_by`, `updated_by`, `created_at`, `updated_at`. `parent_id` is the `local_id` of the parent comment on the
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

On a connection bound to books, `comments.list` and `comments.create` prove the page by one read of it. `get`,
`update`, and `delete` read the comment and prove its page by a second read; only a comment of a page in a bound
book is returned or changed. A comment that is not a page comment is refused. A refusal is an `invalid-request`
that does not name the other book, page, or comment, and no change request is sent. A connection without targets
needs no proof reads.

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

## Export

`bookstack.content.export` returns one page, chapter, or book as text in the answer. Arguments: `type`
(`page`, `chapter`, or `book`), `id`, and `format` (`markdown`, `plaintext`, or `html`). The request is a single
`GET /api/{pages|chapters|books}/{id}/export/{format}`; the path is built only from these fixed values. The text
is untrusted data and limited to 8 MiB: a larger export is refused with a hint to use `bookstack.content.download`.
The answer has `type`, `id`, `format`, and `content`.

`bookstack.content.download` writes the export to `local_path` instead and accepts the formats `html`, `pdf`,
`plaintext`, `markdown`, and `zip`. The file is streamed up to 512 MiB within 10 minutes and is part of no
profile; the connection must release the directory for writing, and an existing file is replaced only with
confirmation. The answer carries `type`, `id`, `format`, `size`, and `sha256` only, never the content. A ZIP export
contains the attachments and images of the exported content.

Both tools need the BookStack role permission `content-export` for the token's user in addition to read access.
On a connection bound to books, a `book` is checked against the books before the secrets are read and before
any request; a `chapter` or `page` is bound through one proof read of the item (without targets this read is
skipped).

## Attachments

Attachments belong to pages. `bookstack.attachments.list` takes an optional `page_id` (required on a
connection bound to books), `limit`, and `offset`, and lists `id`, `name`, `extension`, `page_id`, `external` (true
for a link), `order`, `created_by`, `updated_by`, `created_at`, and `updated_at`, without any content. The filter
sent to BookStack is only an optimization: every row is checked again for the page. BookStack before v26.09.1 also
lists attachments of pages in the recycle bin.

`bookstack.attachments.get` takes the `id` and returns the same fields plus `links` (ready-made html and markdown
links) and, for a link attachment, its target `url`. The target is untrusted data and is never fetched. BookStack
returns the content of a file inside the JSON answer as base64; the tool decodes it as a stream and drops it
without holding it, and the content of the answer is limited to 96 MiB.

`bookstack.attachments.download` takes the `id` and `local_path` and writes the content of a file attachment to
the local file, decoded as a stream, up to 72 MiB within 10 minutes. A link attachment is refused and leaves no
file. The answer carries `id`, `name`, `size`, and `sha256` only, never the content. An existing file is replaced
only with confirmation. Writing needs the connection's local file release for writing.

On a connection bound to books, the page of the attachment is proven to lie in a bound book before anything is
returned or written: the metadata comes first in the answer of BookStack, the page is read once as proof, and only
then the first byte of content is decoded; an attachment of another book is refused as `invalid-request` without
naming the other book, page, or attachment. A connection without targets reads no proof. These three tools are
read-only and idempotent; `list` and `get` belong to the setup profile `read`, `download` does not.

### Changing attachments

`bookstack.attachments.link` takes `page_id`, `name` (1 to 255 characters), and `link` and attaches a link to the
page. The link must be an `http` or `https` URL of 1 to 2000 characters without user information; it is checked
locally before any credential is used, and Qatlas never requests it. `bookstack.attachments.upload` takes
`page_id`, `name`, and `local_path` and attaches one local file of at most 50 MiB. The file lies in a directory the
connection releases for reading and is streamed from disk as `multipart/form-data` with an exact length, never
held in memory; the size is checked before any credential is used, and the transfer may take 30 minutes.

`bookstack.attachments.update` takes the `id` and at least one of `name`, `link` (for a link attachment only;
BookStack refuses it for a file), and `page_id` (moves the attachment to that page; BookStack needs the update
permission on both pages). `bookstack.attachments.replace` takes the `id` and `local_path` and replaces the file of
a file attachment under the same limits as `upload`; the name stays and the previous file is gone. BookStack
accepts a multipart body only on `POST`, so the request is `POST /api/attachments/{id}` with the form field
`_method=PUT`. A link attachment is refused after one metadata read, also on a connection without targets.
`bookstack.attachments.delete` takes the `id` and deletes the attachment; attachments are not kept in the recycle bin,
so the deletion is final.

On a connection bound to books, `link` and `upload` prove the page by one read. `update`, `replace`, and `delete`
find the page of an existing attachment through the attachment listing filtered by `id`, which carries no file
content, and then prove that page by one read; `update` with `page_id` proves the target page as well. A page or an
attachment of another book is refused as `invalid-request` without naming it, and nothing is changed. A connection
without targets reads no proof.

Each call sends exactly one change request without retry; after a timeout, a lost connection, or a 5xx answer the
result is reported as uncertain, and the attachments of the page should be read before repeating it. All five
tools require confirmation and are part of no profile. `link` and `upload` create and are not idempotent; `update`
is idempotent; `replace` updates and is not idempotent; `delete` requires an entry in the `tools` list and the
`delete` permission (see Deleting).

## Images

Gallery images and drawings belong to pages. `bookstack.images.list` takes an optional `page_id` (required on a
connection bound to books), an optional `type` (`gallery` or `drawio`), `limit`, and `offset`, and lists `id`, `name`,
`type`, `page_id`, `url`, `created_by`, `updated_by`, `created_at`, and `updated_at`, without any image data. The
filters sent to BookStack are only an optimization: every row is checked again for page and type.

`bookstack.images.get` takes the `id` and returns the same fields plus `thumbs` (the `gallery` and `display` urls) and
`content` (the ready-made `html` and `markdown` snippets that embed the image). Names, urls, and snippets are
untrusted data and a url is never fetched.

`bookstack.images.download` takes the `id` and `local_path` and writes the image data to the local file, streamed up
to 64 MiB within 10 minutes. The answer carries `id`, `name`, `size`, `sha256`, and `content_type` only, never the
data. An existing file is replaced only with confirmation. Writing needs the connection's local file release for
writing. Downloading by an arbitrary image url is not offered.

On a connection bound to books, the image is bound through its page: the metadata is read first and the page of the
image is read once as proof, and only then is the data requested. An image of another book is refused as
`invalid-request` without naming the other book, page, or image. A connection without targets reads no proof. These
three tools are read-only and idempotent; `list` and `get` belong to the setup profile `read`, `download` does not.

### Changing images

`bookstack.images.upload` takes `page_id`, `type` (`gallery` or `drawio`), `local_path`, and an optional `name` (1 to
180 characters) and adds one image to the page. The file lies in a directory the connection releases for reading, is
at most 50 MiB, and is streamed from disk as `multipart/form-data` with an exact length (fields `type`,
`uploaded_to`, `name`, and the file as `image`). Extension and type are checked before any credential or file is
used: `png`, `jpg`, `jpeg`, `gif`, and `webp` are accepted, a `drawio` drawing only as `png`.

`bookstack.images.update` takes the `id` and a `name` (1 to 180 characters) and renames the image.
`bookstack.images.replace` takes the `id` and `local_path` and replaces the file under the same limits as `upload`;
the name stays and the previous file is gone. The local extension must equal the extension of the existing image
(`jpg` and `jpeg` count as one), otherwise the call is refused before any change. BookStack accepts a multipart body
only on `POST`, so the request is `POST /api/image-gallery/{id}` with the form field `_method=PUT`.
`bookstack.images.delete` takes the `id` and deletes the image: final, without a usage check, and it can leave pages
with broken image references.

On a connection bound to books, `upload` proves the page by one read; `update`, `replace`, and `delete` read the
metadata of the existing image and prove its page by one read. A page or image of another book is refused as
`invalid-request` without naming it, and nothing is changed. A connection without targets reads no proof.

Each call sends exactly one change request without retry; after a timeout, a lost connection, or a 5xx answer the
result is reported as uncertain. All four tools require confirmation and are part of no profile; `delete` also
requires an entry in the connection's `tools` list. `upload` creates and is not idempotent, `update` is idempotent,
`replace` is not.

## Limits

Reading accepts a response of up to 16 MiB (an inline export 8 MiB, a download 512 MiB; an attachment answer 96 MiB of content, an attachment download 72 MiB, an image download 64 MiB); a larger one fails as `invalid-provider-response`. Writing
accepts at most 1 MiB of page content (`html` or `markdown`) and an attachment or image upload of at most 50 MiB. Redirects are never followed: a 3xx answer is a
`provider-error`.

## Deleting

`bookstack.pages.delete`, `bookstack.books.delete`, and `bookstack.chapters.delete` move the object to the
BookStack recycle bin. A book goes with all its chapters and pages, a chapter with all its pages. Only a
BookStack admin can restore them, until the recycle bin is emptied automatically (30 days by default,
`RECYCLE_BIN_LIFETIME`; with `0` the deletion is immediate and final). `bookstack.shelves.delete` removes only
the shelf; its books stay. Each call sends exactly one `DELETE` without retry; after a timeout or a 5xx answer
the result is reported as uncertain.

These tools require a tool allow list and are part of no profile. A connection offers one only when its `tools`
list names it and its `permissions` include `delete`; a connection without a `tools` list does not offer them.
On a connection bound to books, `books.delete` checks `id` against the books before any request, and
`chapters.delete` proves the chapter's book by one read; deleting a bound book is allowed, but the target then
points at nothing. `shelves.delete` is instance-wide and refused on a connection with book targets.
`bookstack.attachments.delete` follows the same rules but is final (see Attachments).

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
