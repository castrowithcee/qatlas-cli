---
description: >
  Describes BookStack page attachment and image tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# BookStack files

Group `files`: attachments and images of pages.

## Attachments

Attachments belong to pages. `bookstack.attachments.list` takes an optional `page_id` (required on a
connection with targets), `limit`, and `offset`, and lists the attachments without any content. The filter
sent to BookStack is only an optimization: every row is checked again for the page. BookStack before v26.09.1 also
lists attachments of pages in the recycle bin.

`bookstack.attachments.get` takes the `id` and returns the same fields plus the ready-made `links` and, for a link attachment, its target `url`. The target is untrusted data and is never fetched. BookStack
returns the content of a file inside the JSON answer as base64; the tool decodes it as a stream and drops it
without holding it, and the content of the answer is limited to 96 MiB.

`bookstack.attachments.download` takes the `id` and `local_path` and writes the content of a file attachment to
the local file, decoded as a stream, up to 72 MiB within 10 minutes. A link attachment is refused and leaves no
file. The answer never carries the content. An existing file is replaced
only with confirmation. Writing needs the connection's local file release for writing.

On a connection with targets, the page of the attachment is proven before anything is returned or written: the
metadata comes first in the answer of BookStack, and only after the proof is the first byte of content decoded.
These three tools are read-only and idempotent; `list` and `get` belong to the setup profile `read`, `download` does not.

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

On a connection with targets, `update`, `replace`, and `delete` find the page of an existing attachment through the
attachment listing filtered by `id`, which carries no file content, and prove that page (see [Targets](README.md#targets)).

Each call sends exactly one change request without retry; after a timeout, a lost connection, or a 5xx answer the
result is reported as uncertain, and the attachments of the page should be read before repeating it. All five
tools require confirmation and are part of no profile. `link` and `upload` create and are not idempotent; `update`
is idempotent; `replace` updates and is not idempotent; `delete` requires an entry in the `tools` list and the
`delete` effect (see [Deleting](README.md#deleting)).

## Images

Gallery images and drawings belong to pages. `bookstack.images.list` takes an optional `page_id` (required on a
connection with targets), an optional `type` (`gallery` or `drawio`), `limit`, and `offset`, and lists the images without any image data. The
filters sent to BookStack are only an optimization: every row is checked again for page and type.

`bookstack.images.get` takes the `id` and returns the same metadata plus the `thumbs` urls and the ready-made
`content` snippets that embed the image. Names, urls, paths, and snippets are
untrusted data and neither a url nor a path is ever fetched.

`bookstack.images.download` takes the `id` and `local_path` and writes the image data to the local file, streamed up
to 64 MiB within 10 minutes. The answer never carries the
data. An existing file is replaced only with confirmation. Writing needs the connection's local file release for
writing. Downloading by an arbitrary image url is not offered.

On a connection with targets, the metadata of the image is read first and its page proven, and only then is the data
requested. These three tools are read-only and idempotent; `list` and `get` belong to the setup profile `read`, `download` does not.

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

On a connection with targets, `update`, `replace`, and `delete` read the metadata of the existing image and prove
its page before the change.

Each call sends exactly one change request without retry; after a timeout, a lost connection, or a 5xx answer the
result is reported as uncertain. All four tools require confirmation and are part of no profile; `delete` also
requires an entry in the connection's `tools` list. `upload` creates and is not idempotent, `update` is idempotent,
`replace` is not.
