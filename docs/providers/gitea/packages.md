---
description: >
  Describes the package tools of the Gitea and Forgejo providers: listing and reading the packages of a user or an
  organization and their files, linking a package to a repository and unlinking it, deleting versions for good, how
  packages are bound to the targets, the differences between the products, and the limits.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Packages

The package tools list and read the packages of one owner, a user or an organization, link a package to a repository
of that owner, and delete versions for good. They hold for Gitea and Forgejo alike unless the table or a line names
one product. Names, versions, and file names in answers are untrusted data.

| Tool | Effect | Provider |
| --- | --- | --- |
| `packages.list`, `packages.get`, `package-files.list` | read | both |
| `package-versions.list`, `package-versions.get-latest` | read | Gitea |
| `packages.link`, `packages.unlink` | update | both |
| `packages.delete-version` | delete, listed-only | both |
| `packages.delete` | delete, listed-only | Gitea |

## Target binding

- Every tool takes `owner` (`users/NAME` or `orgs/NAME`). A connection with targets must name it as an owner target
  of the same scope; a repository target does not allow it, and `users/NAME` does not allow `orgs/NAME`. Without
  targets any owner the token reaches is allowed. The refusal comes before the token is read and does not name the
  value.
- `packages.link` takes `repo_name`, a repository of the same owner, and checks `OWNER/repo_name` against the targets
  as well.
- The owner in the path binds the package. A row or an answer whose `owner` is another or missing is dropped from a
  list and refused as `not-found` by a read.
- A name or version is one escaped path segment, so a slash in it (an npm scope, a container path) stays inside the
  segment. A part between slashes may not be `.`, `..`, or a single `-`, so that an instance or proxy that decodes
  the slash before routing cannot reach another route.

## Behavior

- `type` is one of the package types the product documents. `terraform` exists on Gitea only and is refused on
  Forgejo before the token is read.
- `packages.list` takes `type` and `q`; a row is one package version. `package-versions.list` lists the versions of
  one package.
- `package-files.list` shows at most 200 files and reports `has_more` beyond that. The size is `size` on both
  products, where Forgejo's API calls it `Size`.
- `packages.link` links all versions of a package to the repository; `packages.unlink` removes the link and keeps the
  package.
- `packages.delete-version` and `packages.delete` are final; the second removes every version. Both are offered only
  by a connection whose `tools` list names them, need the `delete` permission and their own confirmation, and are in
  no setup profile.
- The contents of a package are not offered. They travel through the registry protocols of each package type, not
  through the REST API.

## Differences

Forgejo has no `package-versions.list`, `package-versions.get-latest`, or `packages.delete`; they are not registered.
The API documents no difference between the supported versions of either product.
