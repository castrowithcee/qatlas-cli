---
description: >
  Describes the content tools of the Gitea and Forgejo providers: reading files and directories, raw content,
  repository archives, and editor configuration, and writing files as commits, the repository binding, path rules,
  bounds, local files, the commit rules, and the differences between the products.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Contents

The content tools read the files of a repository at a branch, tag, or commit, download a raw file or a repository
archive to a local file, read the editor configuration of a path, and write files as commits. [Forgejo](../forgejo/README.md) names the
differences.

## Target binding

Every path carries the repository, chosen against the connection's targets before the token is resolved; a
repository outside the targets is refused as `invalid-request` without quoting it.

## Paths and refs

A file path is untrusted data. It is checked before any request: relative, without empty segments, `.`, `..`,
backslashes, or control characters. Each segment is sent as its own escaped path segment below one fixed route. A
`ref` follows the rules of the [branch names](branches.md#branch-names); it is the default branch when left out.

## Behavior

- `contents.list` and `contents.get` return the entry of a file or the entries of a directory with every member of
  the contents object. Text is decoded from Base64 and cut; a binary file keeps its size and hash but returns no
  content. Directory lists are cut and say so in `truncated`.
- `contents.get-ext` and `contents.get-many` (Gitea only) return the same entries. `get-ext` sends only metadata
  unless `includes` asks for more. `get-many` reads several files with a POST that only selects them; nothing is
  changed.
- `contents.get-raw` returns the text of the raw route, or of the LFS route with `media`, cut; a binary file returns
  only its size.
- With `local_path`, `contents.get-raw` and `archives.get` stream the file into a directory the connection releases
  for writing. A redirect to another address is refused, no partial file is left, and an existing file is replaced
  only with confirmation. Because of that, `contents.get-raw` is offered only to a connection that releases a
  directory for writing, also for reading text.
- `editorconfig.get` returns the settings that apply to a path. Gitea may answer without a body, which shows as an
  empty `config`.
- Names, paths, contents, and settings are untrusted data.

## Writing

Each write tool sends exactly one commit request and never repeats it: after an open outcome (timeout, reset, 5xx)
it reports that the change may have been applied, and the state must be read before trying again. A refusal with
400, 409, or 422 is reported without the instance's text, as a missing or stale `sha`, an existing or missing path,
a protected or moved branch, or a patch that does not apply.

- `contents.create` and `contents.update` write one file. `contents.update` needs the `sha` of the version being
  replaced, as `contents.get` reports it; Gitea would create the file without it, so Qatlas requires it. A repeated
  update carries a stale `sha` and is refused, which makes it idempotent in effect.
- `contents.change-files` writes up to 50 files as one commit, each path once. Every operation is `create`,
  `update` (needs `sha`), and on Gitea also `upload` (create or replace) and `rename` (needs `from_path`, content
  optional). A batch never deletes: `delete` is not an operation, and `contents.delete` removes one file per call,
  needs its `sha`, is offered only to a connection whose `tools` list names it, and is in no profile.
- `contents.apply-diff` applies a unified diff as one commit. Forgejo documents `sha` as required for this route;
  Qatlas does not send it.
- Content is text (`content`, up to 256 KiB) or a local file (`local_path`, from a directory the connection
  releases for reading), exactly one of them per file, and 8 MiB together per call. Both are read and bounded
  before the token is resolved, and a local path is never quoted in an error. Because of that, `contents.create`,
  `contents.update`, and `contents.change-files` are offered only to a connection that releases a directory for
  reading, also for text.
- `branch` is the base and target, `new_branch` creates a branch from it first. Both follow the
  [branch names](branches.md#branch-names). `force_push` (Gitea) and `force_overwrite_new_branch` (Forgejo) are not
  offered.
- `author`, `committer` (name and email), and `dates` are sent only when named; otherwise the commit carries the
  identity of the token. The instance takes one for the other when only one is named. `signoff` adds the
  committer's sign-off line.
- The answer carries the written files, the commit, and its signature verification, without the content and
  without the raw signature.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `contents.list` | read | Gitea, Forgejo |
| `contents.get` | read | Gitea, Forgejo |
| `contents.get-ext` | read | Gitea |
| `contents.get-many` | read | Gitea |
| `contents.get-raw` | read | Gitea, Forgejo |
| `archives.get` | read | Gitea, Forgejo |
| `editorconfig.get` | read | Gitea, Forgejo |
| `contents.create` | create | Gitea, Forgejo |
| `contents.update` | update | Gitea, Forgejo |
| `contents.change-files` | update | Gitea, Forgejo |
| `contents.apply-diff` | update | Gitea, Forgejo |
| `contents.delete` | delete, listed only | Gitea, Forgejo |
