---
description: >
  Describes the wiki tools of the Gitea and Forgejo providers: scopes, repository binding, page names, text content
  and its cap, a disabled wiki, revisions, and the listed-only deletion.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Wiki

Both products serve these tools through one code base. `wiki-pages.list`, `.get`, `.create`, `.update`, and
`.delete` and `wiki-revisions.list` take `repository` and are bound to the connection's targets like every
repository tool. The Git repository behind the wiki and the repository settings of the wiki are not covered here.

## Scopes

A read needs `read:repository`, a change `write:repository`; the wiki routes sit in the repository route group of
the API.

## Page names

- A page is addressed by `page_name`, the `sub_url` the instance reports, not by `title`. A name may hold `/`; it
  travels percent-encoded as `%2F` inside one path segment, which the client leaves as sent.
- A name has at most 255 bytes, is UTF-8 without control characters, and is not `.` or `..`. Nothing else is
  checked; the instance decides which names exist.
- `wiki-pages.create` names the new page with `title`; `wiki-pages.update` renames with an optional `title`.

## Content

- The API carries Base64; the tools take and return text. Input is valid UTF-8 text of at most 256 KiB and
  replaces the whole page on update. Writes never echo the content.
- `wiki-pages.get` cuts the text at 256 KiB and then sets `truncated`; there is no pagination. Content that is not
  valid UTF-8 text is left out and `binary` is set. Sidebar and footer are cut at 64 KiB. All page text is
  untrusted data.
- `message` is the commit message: at most 4096 bytes without control characters other than line breaks.

## Disabled wiki

A repository with a disabled wiki answers 404 like a missing page, and 403 and 404 are one `not-found` class.
The refusal says that the wiki may be disabled, without quoting the instance.

## Revisions

`wiki-revisions.list` takes `page_name` and `cursor` but no `limit`: the instance serves a fixed batch and takes
only `page`. Since an administrator may change that batch size, the end compares the revisions read so far with
`X-Total-Count`, with the `count` of the answer as fallback. The commit member of
the committer is spelled `commiter` by the API and kept so.

## Deleting

`wiki-pages.delete` removes a page for good and is offered only to a connection whose `tools` list names it; no
setup profile holds it.
