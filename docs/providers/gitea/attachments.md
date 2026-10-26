---
description: >
  Describes the attachment tools of the Gitea and Forgejo providers: listing, reading, uploading, renaming, deleting,
  and downloading the attachments of an issue, a pull request, or a comment; how the owner and the attachment are
  bound to the repository, the local-file rules of upload and download, the listed-only tools, and the product
  differences.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Attachments

The attachment tools act on the files attached to an issue or pull request (`issue-attachments.*`, addressed by
`number`) or to a comment (`comment-attachments.*`, addressed by the comment `id`) of a repository inside the
connection's targets. The attachments an issue or comment shows in its own `assets` member are the same objects.

## Binding

- `repository` is chosen against the connection's targets before the token is resolved or a request is sent.
- Every tool first reads the owner. An issue or pull request must report a canonical repository inside the targets;
  a comment must name the chosen repository through its issue or pull request address, as in [Comments](comments.md).
  Otherwise the call is refused as `invalid-request` before the attachment is touched.
- `attachment_id` must be one of the attachments the owner lists in `assets`; an attachment of another issue or
  comment is refused as `invalid-request` before any further request, and an attachment answer must carry the ID.
  The binding is proved by Qatlas, not left to the instance's route.
- Names are untrusted data. File paths never appear in error messages.

## Tools

- `*.upload` streams a local file from a directory the connection releases under `files.read`; path and size (at most
  2 GiB) are checked before the credential is resolved. The attachment is named by `name`; the local file name is not
  sent. The instance may refuse a file type or size its attachment settings do not allow. A link to an external
  address is never created (Forgejo's `updated_at` is not offered either).
- `*.update` changes only `name`.
- `*.download` writes the content to a file in a directory released under `files.write`; an existing file is replaced
  only with confirmation, and a refused or failed download leaves no file. The result carries `local_path`, `size`,
  and `sha256`, never the content. It reads `browser_download_url` of the attachment and fetches it only when it is
  exactly `/attachments/UUID` with the attachment's own UUID on the scheme, host, and port of `base_url`. Any other
  address is refused as `invalid-request`, and a redirect is not followed. Both products serve every uploaded
  attachment, whichever object it belongs to, from this one route and accept the access token header there; this
  was checked against their sources, not against a live instance. An instance that serves attachments from object storage answers with a redirect, so its attachments
  cannot be downloaded through Qatlas.
- `*.delete` removes an attachment for good; it is offered only by a connection whose `tools` list names it and is in
  no setup profile.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-attachments.list`, `.get`, `.download` | read | both |
| `issue-attachments.upload` | create | both |
| `issue-attachments.update` | update | both |
| `issue-attachments.delete` | delete, listed only | both |
| `comment-attachments.list`, `.get`, `.download` | read | both |
| `comment-attachments.upload` | create | both |
| `comment-attachments.update` | update | both |
| `comment-attachments.delete` | delete, listed only | both |

What differs on Forgejo is listed in [Forgejo](../forgejo.md).
