---
description: >
  Describes the release tools of the Gitea and Forgejo providers: reading, creating, changing, and deleting the
  releases of a bound repository, their scopes, how a release is addressed by ID or tag, the differences between the
  products, and the limits.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Releases

The release tools read, create, and change the releases of one repository inside the connection's targets, and delete
them for good. They hold for Gitea and Forgejo alike unless a line names one product. Assets are read only as the list
a release carries; the asset tools and the tag tools are described elsewhere.

## Scopes

The tools presumably need `read:repository` to read and `write:repository` to change, since the API serves releases
in the repository route group. None was checked against a live instance; a missing scope shows as `not-found`.

## Target and ID binding

Every tool takes `repository` (`OWNER/REPO`), checked against the targets before the credential is resolved and
before any request; it may be left out only when the targets name exactly one repository.

A release is addressed below the repository's own path, by `release_id` or by `tag`, never by a free path. The `url`
member of a release is its API address and names the repository and the ID. Every release read checks it by canonical
path without case: a release without `url`, or whose `url` names another repository or ID, is refused as
`invalid-request` without naming it. `releases.get` also refuses an answer whose `id` is not the one asked for.
`releases.update`, `releases.delete`, and `releases.delete-by-tag` first read the release (GET by ID or tag) and send
their one change only after this check passes. `releases.list` drops rows that fail it. A repository that was renamed
answers with a redirect, which Qatlas does not follow.

A `tag` is a path segment: it must be a valid Git tag name of at most 255 characters and may not contain a slash.
A release whose tag contains a slash is reached by its ID. A tag name in the body of `releases.create` and
`releases.update` may contain slashes.

## Behavior

- `releases.list` reads one batch, newest first. `draft` and `prerelease` filter; a draft is shown only to a token
  with write access. Forgejo takes `q`; Gitea takes `tag_filter` from 28.2.0 on, and an older instance refuses it
  before the list request.
- `releases.get-latest` reads the latest release that is neither a draft nor a pre-release.
- `releases.create` creates the Git tag at `target_commitish` when it does not exist, so it can add a tag to the
  repository. Gitea takes `tag_message` for an annotated tag; Forgejo takes `hide_archive_links`, which
  `releases.update` takes as well. The other product's member is refused as `invalid-request` before the secret.
- `releases.update` changes only the members passed, by ID, after the evidence read.
- `releases.delete` and `releases.delete-by-tag` remove the release and its assets for good. Whether the instance keeps
  the Git tag is its decision. Both are offered only by a connection whose `tools` list names them, and are in no
  setup profile.
- A release carries `author` (personal data), its `assets`, and, on Forgejo, `hide_archive_links` and
  `archive_download_count`. A source archive address or upload address with user info or a query is left out. Names,
  notes, and tags are untrusted data.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `releases.list`, `releases.get`, `releases.get-latest`, `releases.get-by-tag` | read | both |
| `releases.create` | create | both |
| `releases.update` | update | both |
| `releases.delete`, `releases.delete-by-tag` | delete, listed only | both |
