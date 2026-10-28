---
description: >
  Describes BookStack book, chapter, shelf, page, tag, search, and export tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# BookStack content

Group `content`: books, chapters, shelves, tags, pages, search, and export.

## Books and chapters

`bookstack.books.list` and `bookstack.chapters.list` take `limit` and `offset` and page through the instance
in records of at most 500, sorted by `id`. `bookstack.books.get` and `bookstack.chapters.get` take an `id`.
All four only read. `chapters.list` takes an optional `book_id`. On a connection with targets `books.list`
returns only the reached books (see [Targets](README.md#targets)). A listing stops as soon as the instance returns no new record.

`books.get` lists `contents`: the chapters with their pages and the loose pages in book order. `chapters.get`
lists its `pages`. `default_template_id` is 0 when there is none. Tags are limited to 50. A content tree or page list holds at most 2000 entries in total;
`truncated` is then `true`. Users are shown as BookStack delivers them: lists and the entries of `pages[]` and
`books[]` carry only the identifier, so a user appears there with `id` only; a detail answer shows `id`, `name`,
and `slug`. A user field a list already shows with a name (such as `owned_by` in `pages.list`) keeps it. On a
connection with targets, `book_slug` is reported only when the book itself is bound; otherwise only `book_id` is
reported. A book on a bound shelf counts as bound only when the call has read that shelf anyway. Descriptions
are cut to 20000 characters and other strings to 1000. Descriptions are untrusted data, `description_html` is HTML that must never be rendered or
run. `books.get` shows the `shelves` of the book only on a connection without
targets, because shelves span the whole instance. `books` and `shelves` report the cover image as an address of the instance; the addresses are untrusted data and are never fetched.

## Writing books and chapters

`books.create` and `books.update` take `name` (1 to 255 characters), at most one of `description` (plain text,
at most 1900 characters) and `description_html` (at most 2000 characters), `tags`, and `default_template_id`.
`chapters.create` and `chapters.update` take the same fields and `priority` (0 or more); `chapters.create`
requires `book_id` and `name`, `chapters.update` requires only `id`. `books.update` and `chapters.update`
need at least one field to change. A violation of these limits is refused locally as `invalid-request` before
any secret is read and before any request. A description cannot be emptied through these tools. For deleting
see [Deleting](#deleting), for cover images see [Cover images](#cover-images).

- `tags` replaces all existing tags; an empty list removes them. Omit `tags` to keep them.
- `default_template_id` is the identifier of a template page. On `update`, `null` removes the default
  template. BookStack validates that the page is a template the token may see.
- `chapters.update` with `book_id` moves the chapter into that book. Moving needs more than updating; see
  [Content](setup.md#content).

On a connection with targets or exclusions, the book, the chapter, a target `book_id`, and a `default_template_id`
are proven before the change (see [Targets](README.md#targets)). Excluded chapters and pages are left out of the
content trees and lists.

The tools are not part of any profile. All four require confirmation; the creating tools are not idempotent.
Each change is sent once and never retried; the result shows the changed object without its content tree.

## Shelves

Shelves hold any books and span the whole instance, so the shelf tools work only on a connection without
targets; `shelves.get` also reads a bound shelf (see [Targets](README.md#targets)). An excluded shelf is left out of the
list and refused by every shelf tool; `books` of a shelf, in `shelves.get` and as an argument, follows BookStack's
assignment without the excluded books: they are not shown, not sent, and an update keeps those already on the shelf.

`bookstack.shelves.list` takes `limit` and `offset` and pages through the instance in records of at most 500,
sorted by `id`. `bookstack.shelves.get` takes an `id` and lists `books` in shelf order (at most 2000 entries;
`truncated` is then `true`) and at most 50 tags. Users follow the rule under
[Books and chapters](#books-and-chapters). Names, descriptions, and tags are untrusted data; `description_html` must
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
part of no profile; `create` is not idempotent. Each change is sent once and never retried. For deleting see
[Deleting](README.md#deleting).

### Cover images

`bookstack.books.set-cover` and `bookstack.shelves.set-cover` take `id` and `local_path` and set the cover image
from a local file (png, jpg, jpeg, gif, or webp, at most 50 MiB, streamed from disk from a directory the
connection releases for reading). The file type is checked before any secret is read or any file is opened. The
request is a multipart `POST` with `_method=PUT` and the file in the field `image`. `books.update` and
`shelves.update` take `remove_cover: true` to remove the cover (sent as `image: null`); it can be combined with
the other fields and is a valid change on its own, and `false` sends nothing. On a connection with targets the
book is proven and the shelf variants are refused (see [Targets](README.md#targets)). All of these require
confirmation, are not part of any profile, send one request without retry, and `set-cover` is not idempotent.

## Tags and instance information

`bookstack.tags.list` lists the tag names used on content the token can see. It takes
`limit`, `offset`, optional `name_contains`, and `sort`. `bookstack.tags.values` takes a required `name` and optional
`value_contains`, `limit`, `offset`, and `sort` and lists that name's values. Tags are sorted by name (values by value) unless `sort`
says otherwise; they take no `filters`, because BookStack filters tags by name and value only, which the contains arguments cover. The contains
arguments (at most 255 characters, case-insensitive) are sent as a `%...%` filter in which `%`, `_`, and `\` are
masked as literals; every returned row is checked against the filter again, because BookStack ignores filters
it does not know. Tags aggregate over the whole instance, so both tools are refused locally on a connection with
targets, like the shelf tools. Names and values are untrusted data. Tags are written through the items that
carry them. A connection with exclusions may use both tools, but the catalog counts every tag of the instance:
names, value counts, and usages can include excluded content.

## Pages

`pages.get` returns `html` (rendered), `markdown` (only for pages saved with the Markdown editor), and `raw_html`
(as stored, what the editor shows); `chapter_id` is 0 when there is none, and tags are limited to 50. Users follow the rule under
[Books and chapters](#books-and-chapters). The comment tree of a page reports at most 500 comments and 10 levels; a cut tree is marked `comments_truncated`, and each comment text is capped like in `comments.get`. Comment texts are personal and untrusted data. All content, tags, and names are untrusted data; `html` and
`raw_html` must never be rendered or run.

Page includes (`{{@ID}}`, `{{@ID#section}}`) are resolved by BookStack in `html` up to three levels. On a
connection with targets an include of a page outside them is empty in `html` on every level, as for a user
without read access; `raw_html` and `markdown` keep the placeholder, and writing one stays allowed. For a page
with such an include Qatlas builds `html` itself from the stored HTML by BookStack's insertion rules. Limit:
that `html` is unfiltered like `raw_html`, because BookStack's content filter, unique heading ids, and theme
hooks cannot be reproduced; every other page keeps BookStack's `html` unchanged. Includes beyond what one call
checks leave `html` empty and set `html_withheld`; after `pages.create` or `pages.update` a failed check does the
same instead of failing the write, which has taken effect.

`revision_count` and `editor` of a page can be neither filtered nor sorted, because BookStack does not offer them as listing fields.

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
Moving needs more than updating; see [Content](setup.md#content). On a connection with
targets the source page and the target are proven before the change (see [Targets](README.md#targets)).

## Search

`bookstack.content.search` finds shelves, books, chapters, and pages with the BookStack search. Arguments:
`query` (required, 1 to 1000 characters), at most one of `book_id` and `chapter_id`, `page` (from 1, default 1),
and `count` (1 to 100, default 20). Without `book_id` and `chapter_id` the search covers the instance
(`GET /api/search`); with `book_id` one book (`GET /api/search/book/{id}`); with `chapter_id` one chapter
(`GET /api/search/chapter/{id}`). `query` is sent only as a URL-encoded query parameter of that endpoint;
beside `page` and `count` no other parameter is sent. The
[BookStack search syntax](https://www.bookstackapp.com/docs/user/searching/) (`{type:page}`, `[tag=value]`,
`{created_by:me}`, and so on) is allowed because it only filters within the chosen endpoint.

On a connection with targets, `book_id` and `chapter_id` are proven and the defaults of [Targets](README.md#targets) apply. Every hit
is checked on the client; shelves, hits without a book, and hits outside the targets are dropped, so a page can
hold fewer hits than `count`.

A hit carries `tags` (at most 50) and previews (`preview_name`, `preview_content`). The previews are untrusted HTML, cut to 2000 characters; never
render or run them. `total` is the number BookStack reports and is an estimate. On a connection with targets, `book` and `chapter` of a hit carry only `id` unless that book or chapter is bound.

## Export

`bookstack.content.export` returns one page, chapter, or book as text in the answer. Arguments: `type`
(`page`, `chapter`, or `book`), `id`, and `format` (`markdown`, `plaintext`, or `html`). The request is a single
`GET /api/{pages|chapters|books}/{id}/export/{format}`; the path is built only from these fixed values. The text
is untrusted data and limited to 8 MiB: a larger export is refused with a hint to use `bookstack.content.download`.

`bookstack.content.download` writes the export to `local_path` instead and accepts the formats `html`, `pdf`,
`plaintext`, `markdown`, and `zip`. The file is streamed up to 512 MiB within 10 minutes and is part of no
profile; the connection must release the directory for writing, and an existing file is replaced only with
confirmation. The answer never carries the content. A ZIP export
contains the attachments and images of the exported content.

BookStack inserts page includes (`{{@ID}}`, see [Pages](#pages)) into every export format, `markdown` too, by
what the token may read. On a connection with targets both tools therefore read the stored HTML of every page
the export contains (the page, the pages of a chapter, all pages of a book), however many, and of the pages
they include up to three levels, once per page and call. They refuse the export as `invalid-request` before any
export request if one include names a page outside the targets, or if the includes need more reads than one
call allows (50 pages that are only included). A `rate-limited` answer while reading is reported as such; export
smaller items then. A connection without targets reads nothing extra. Images of other pages are embedded by
BookStack by its own storage rights; Qatlas does not check them.

Both tools refuse an excluded item, and a book or chapter that holds an excluded chapter or page, before any
export request. The token rights are listed under [Content](setup.md#content). On a connection with targets or
exclusions the item is proven first (see [Targets](README.md#targets)).

## Deleting

`bookstack.pages.delete`, `bookstack.books.delete`, and `bookstack.chapters.delete` move the object to the
recycle bin. A book goes with all its chapters and pages, a chapter with all its pages; on a connection with
exclusions a book or chapter that holds an excluded chapter or page is refused, which Qatlas finds in the content
tree it reads for the proof. A move into or out of a book or chapter stays allowed when source and target are in
reach. `bookstack.shelves.delete` removes only the shelf; its books stay, and an excluded shelf is refused. The
general rules are in [Deleting](README.md#deleting).
