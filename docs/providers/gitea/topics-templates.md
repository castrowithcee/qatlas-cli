---
description: >
  Describes the topic tools of a repository and the instance-wide topic search, and the read-only gitignore,
  label, and license templates of Gitea and Forgejo: bindings to the targets, limits, and caps.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Topics and templates

Both products serve these tools through one code base; the tool IDs carry the provider as prefix.

## Topics

- `repository-topics.list`, `.set`, `.add`, and `.remove` take `repository` and are bound to the connection's
  targets like every repository tool.
- A topic name is trimmed and lowercased like the instance does, then must be letters, digits, hyphen, and dot,
  start with a letter or digit, and have at most 35 characters; anything else is refused before the token is read.
  A repository holds at most 25 topics.
- `repository-topics.set` replaces all topics with the given list, which may be empty and then removes them all;
  empty names and repeats are dropped like the instance does. It sends one request. `add` and `remove` touch one
  topic only.
- `topics.search` reads the topics of the whole instance, which no repository target bounds. Only a connection
  without targets offers it; with targets it is refused before any request or token read.
- Topic names are untrusted data.

## Templates

- The gitignore, label, and license template tools read reference data of the instance. They name no repository
  and are usable on every connection, targets or none.
- A template name is letters, digits, and `+ . _ -`, starting with a letter or digit, at most 100 characters; it
  travels as one path segment. `license-templates.get` takes the `key` that `license-templates.list` reports.
- Template texts are untrusted data. A gitignore `source` and a license `body` are cut at 64 KiB (`truncated` is
  then true), a license `implementation` at 8 KiB. The lists hold at most 1000 entries, a label template at most
  200 labels.
- `repository-licenses.list` exists on Gitea only; see [Not on Forgejo](../forgejo.md#not-on-forgejo).

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `topics.search` | read | Gitea, Forgejo |
| `repository-topics.list` | read | Gitea, Forgejo |
| `repository-topics.set` | update | Gitea, Forgejo |
| `repository-topics.add` | update | Gitea, Forgejo |
| `repository-topics.remove` | update | Gitea, Forgejo |
| `gitignore-templates.list`, `gitignore-templates.get` | read | Gitea, Forgejo |
| `label-templates.list`, `label-templates.get` | read | Gitea, Forgejo |
| `license-templates.list`, `license-templates.get` | read | Gitea, Forgejo |
| `repository-licenses.list` | read | Gitea |
