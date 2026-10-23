---
description: >
  Describes the content-reading tools of the Gitea and Forgejo providers: files and directories, raw content,
  repository archives, and editor configuration, the repository binding, path rules, bounds, the local file
  downloads, and the differences between the products.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Contents

The content tools read the files of a repository at a branch, tag, or commit, download a raw file or a repository
archive to a local file, and read the editor configuration of a path. [Forgejo](../forgejo.md) names the
differences.

## Scopes

All tools need `read:repository` (presumably; not checked against a live instance).

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
