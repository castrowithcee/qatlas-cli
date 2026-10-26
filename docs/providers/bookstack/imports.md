---
description: >
  Describes the BookStack ZIP import tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# BookStack imports

The group `imports` uploads BookStack ZIP exports, shows what BookStack detected in them, and imports them as a
book, chapter, or page. Imports
belong to the token's user, and importing a book creates a new book, so the five tools are instance-wide and refused on
a connection with targets, before any secret is read. None of them is part of a profile. The token rights are listed under [Imports](setup.md#imports).

`bookstack.imports.list` lists the pending imports (`limit`, `offset`, `sort`, `filters`). `bookstack.imports.get` returns the
same fields for one `id` plus `details`, a reduced form of what BookStack found in the ZIP: counts, at most 25
`chapter_names`, and the chapters with their pages and the top-level pages in `contents`. The chapters and pages
of `contents` share a budget of 2000 entries; a cut is marked by `contents_truncated`. The structure of
attachments, images, and tags inside an import is not documented by BookStack; they are reported as counts only.
The details and names are untrusted data; the storage path of the file is never shown.

`bookstack.imports.upload` takes `local_path`, a `.zip` file of at most 50 MiB inside a directory the connection
releases for reading, and sends it as a streamed multipart upload in the field `file`. The extension and size are
checked before a secret is read. If BookStack rejects the file, Qatlas' message only says so and points to
`imports.get` and a new upload; BookStack's validation messages follow separately as its original answer
(see [Errors](README.md#errors)). Nothing is imported yet.

`bookstack.imports.run` imports one pending import by `id`. It reads the import first and checks the arguments
against its type before sending anything: a `book` import takes no `parent_type` or `parent_id`; a `chapter`
import needs `parent_type` `book` and the `parent_id` of that book; a `page` import needs `parent_type` `book`
or `chapter` and its `parent_id`. The result is the `type` and `id` of the created object.

`bookstack.imports.delete` discards one pending import by `id`; content created by an earlier run stays.

Upload and run are not idempotent and ask for confirmation. Each sends exactly one request without retry; after
a timeout or a 5xx answer the result is reported as uncertain. `delete` additionally requires a tool allow list
that names it and the `delete` effect.
