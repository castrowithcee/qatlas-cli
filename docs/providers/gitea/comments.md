---
description: >
  Describes the comment tools shared by the Gitea and Forgejo providers: reading, adding, editing, and deleting
  comments on the issues and pull requests of a bound repository, the scopes they need, how a comment ID is bound
  to its repository, the bounds of the lists, and what is deliberately left out.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Comments

The comment tools read and change the comments of the issues and pull requests of one repository that lies
inside the connection's targets. Comments on issues and on pull requests are both reachable; the index of either
works. Every tool takes `repository` (`OWNER/REPO`), which is checked against the targets before the token is
resolved. It may be left out when the targets name exactly one repository. Comment texts, names, and attachment
names are untrusted data, and authors are personal data.

| Tool | Effect | Provider |
| --- | --- | --- |
| `comments.list`, `comments.list-repository`, `comments.get` | read | both |
| `comments.create` | create | both |
| `comments.update` | update | both |
| `comments.delete` | delete, listed only | both |

## Scopes

Reads presumably need `read:issue`, changes presumably `write:issue`; a change by ID also reads the comment
first. The scopes are not confirmed against a live instance.

## Binding a comment ID to its repository

`comments.get`, `comments.update`, and `comments.delete` take a comment ID without an issue index. The ID is
addressed under the chosen repository's own path, and the tools do not rest on the instance enforcing that:
each reads the comment first, and the `issue_url` (an issue) or `pull_request_url` (a pull request) of the answer
must end in `/repos/OWNER/REPO/issues/N` or `/repos/OWNER/REPO/pulls/N` and name the chosen repository. The
comparison ignores case. A comment without such an address, with addresses that disagree, or from another
repository is refused as `invalid-request` without naming the other repository, and nothing is changed. An
answer whose `id` differs from the requested one is an invalid response.

`comments.create` and the lists are bound by the repository path and the issue index alone. Every row of a list
is checked like a single comment, and a row that does not prove its repository is dropped.

## Lists

- `comments.list` reads one issue or pull request. The instance does not page this list; the tool returns at most
  100 comments and sets `truncated` when more exist. `since` and `before` (RFC 3339, on the time of the last
  update) narrow it.
- `comments.list-repository` reads all comments of the repository, with `since` and `before`, in batches that
  follow the common paging of the provider.

## Changes

`comments.create` sends the text as the comment and adds a new comment with every call. `comments.update`
replaces the text; the text must be 1 to 65536 characters. `comments.delete` removes the comment for good and is
offered only by a connection whose `tools` list names it; it is in no setup profile.

## Left out

- The `updated_at` member that Forgejo accepts when a comment is created or edited: it sets the time shown for
  the comment and so rewrites the history of a conversation.
- The path variants `issues/{index}/comments/{id}` for editing and deleting, which the instances mark as
  outdated; the paths under `issues/comments/{id}` are enough.
