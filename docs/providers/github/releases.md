---
description: >
  Describes the GitHub release tools: listing and reading the releases of a repository and the metadata of their assets, creating and updating a release, and deleting one, with their limits and tokens.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub releases

The release tools of the [GitHub provider](README.md). Every tool takes `repository` as described under
[the target of a call](README.md#the-target-of-a-call); a change needs the confirmation described under
[confirmed changes](README.md#confirmed-changes).

## Releases

The tools list, read, and change the releases of a repository and the metadata of their assets; asset
content, tags, and release-notes generation without a release stay out of scope. The five reads and changes
are offered by the not-recommended setup profile `releases`; deleting is in no profile, since it is offered
only where a connection's `tools` list names `github.releases.delete`. Every route lies below the chosen
repository; no tool accepts an owner or a free REST path.

| Tool | Does |
| --- | --- |
| `github.releases.list` | lists compact releases, in GitHub's own order |
| `github.releases.get` | reads one release by `id`, `tag`, or `latest` |
| `github.release-assets.list` | lists the asset metadata of one release |
| `github.releases.create` | cuts one release from `tag` |
| `github.releases.update` | replaces fields of one release; left-out fields stay |
| `github.releases.delete` | deletes one release by `id`; listed only |

```yaml
connections:
  release-reader:
    service: github
    credential: github-reader
    target: repos/octo-org/example
    tools: [github.releases.list, github.releases.get, github.release-assets.list]
```

`github.releases.list` pages by GitHub's `Link` response header, like the Actions and pull request lists that
carry no total count. A release `name` is untrusted data. A draft appears only while the configured token can
see it. `github.releases.get` takes exactly one of `id`, `tag`, or `latest`; a lookup by `tag` never finds a
draft, because GitHub does not index a draft's tag, so a draft is only reachable by `id`. It adds the full
body (untrusted data), `target_commitish`, and `assets_count`.

`github.release-assets.list` reads only metadata, with the name and label untrusted data; an asset's content
is never read.

### Changes

`github.releases.create` requires `tag`; when the tag does not exist yet, GitHub creates it at `target` (a
branch or a commit) once the release is published. A second create for a tag that already has a release is
refused as `invalid-request` naming the tag, without a second attempt, since GitHub holds at most one release
per tag. `github.releases.update` writes only the fields given; `draft: false` publishes a draft, and
`draft: true` converts a published release back to one. `make_latest` takes `"true"`, `"false"`, or
`"legacy"`, exactly as GitHub's REST field does.

`github.releases.delete` deletes one release by `id`, with its release notes and asset attachments; the tag
it was cut from stays. It is high risk, on the scale of the
[pull request merge](pull-requests.md#merge-and-conflicts): a connection offers it only while its `tools` list
names it, whatever its `effects`, and no profile a new connection starts with, recommended or not, may select
it. A release already deleted answers `not-found` naming it, like the other delete tools in this provider.

### Tokens for releases

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.releases.list`, `get`, `github.release-assets.list` | `repo` for a private repository; `public_repo` for a public one | Contents: read |
| `github.releases.create`, `update`, `delete` | `repo` | Contents: read and write |
