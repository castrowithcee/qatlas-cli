---
description: >
  Describes the star and watch tools of the Gitea and Forgejo providers: the starred and watched repositories of an
  account, starring and watching a repository, and the stargazers and watchers of a repository. It covers the binding
  to the targets, the personal-data rules, and the limits.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Stars and watching

These tools read and change which repositories the account behind the token stars or watches, and read who stars or
watches a repository. Both products serve the same routes, fields, and filters; [Forgejo](../forgejo/README.md) has no
deviation here. Issue and pull request subscriptions are a separate group, see
[Issue subscriptions](issue-subscriptions.md).

| Tool | Effect | Provider |
| --- | --- | --- |
| `stars.list`, `stars.check` | read | both |
| `stars.add`, `stars.remove` | update | both |
| `repository-stargazers.list`, `repository-subscribers.list` | read | both |
| `repository-subscriptions.get` | read | both |
| `repository-subscriptions.set`, `repository-subscriptions.delete` | update | both |
| `subscriptions.list` | read | both |

## Target binding

- `stars.check`, `stars.add`, `stars.remove`, and the `repository-*` tools take `repository` (`OWNER/REPO`), checked
  against the targets before the secret is resolved and before any request; it may be left out when the targets name
  exactly one repository. The refusal does not name the repository.
- `stars.list` and `subscriptions.list` read the account behind the token, or another user with `username`. Every
  row is checked against the targets by its canonical `full_name`; rows outside them are dropped. A `username` must
  lie inside the targets as an owner or through one of its repositories and is refused before the token is read
  otherwise. A page may therefore hold fewer rows than `limit` and still have a successor; `has_more` comes from
  the instance's `Link` header or `X-Total-Count`, not from the row count.
- Every change acts on the account behind the token only. No tool changes the star or watch of another user.

## Reading

- The lists show every member of the repository object. `repository-stargazers.list` and
  `repository-subscribers.list` show every member of the user object of each person; names and texts are untrusted
  personal data.
- `stars.check` and `repository-subscriptions.get` report a 404 as `false` (`starred`, `subscribed`), although the
  instance also answers it for a repository the token does not reach; a 403 and any other failure is an error.

## Changing

- `stars.add` and `stars.remove` report `starred` or `unstarred`; `repository-subscriptions.set` returns the watch
  object after the change and `repository-subscriptions.delete` reports `unsubscribed`. A repeat reaches the same
  state. The API has no option to ignore a repository, so `set` sends no body.
- Every change needs confirmation and sends exactly one request. None is repeated after a timeout, an abort, a 5xx
  answer, or an unreadable answer; the error then says the change may have been applied.
