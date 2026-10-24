---
description: >
  Describes the Gitea and Forgejo tools for existing accesses: reading and deleting webhooks of a repository, an
  organization, or the account behind the token, reading and resetting server-side Git hooks, and reading, syncing,
  and deleting push mirrors. It covers the token scopes, the binding to the targets, who the instance serves,
  what is never shown, and why nothing creates or changes an access.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Accesses

These tools read what already reaches into or out of a repository, an organization, or the account: webhooks,
server-side Git hooks, and push mirrors. They create nothing and change no target. Addresses, names, branch
filters, scripts, and error texts in answers are untrusted data; addresses are shown and never fetched.
[Forgejo](../forgejo.md) names the differences.

## Scopes and who is served

Both products serve the routes in these categories; every tool needs the matching `read:` scope, a change or
execution the `write:` scope.

| Tools | Scope | The instance serves |
| --- | --- | --- |
| `webhooks.*` of a repository | `repository` | repository administrators, while webhooks are enabled |
| `webhooks.*` of an organization | `organization` | organization owners, while webhooks are enabled |
| `webhooks.*` of the account | `user` | the account, while webhooks are enabled; Gitea: no public-only token |
| `git-hooks.*` | `repository` | repository administrators allowed to edit Git hooks, while Git hooks are enabled |
| `push-mirrors.*` | `repository` | repository administrators |

An instance that does not serve a caller answers 403 or 404, reported as `not-found`. The push mirror tools also
fail while the instance has mirroring switched off.

## Target binding

- Webhook tools take exactly one of `repository`, `organization`, and `account: true`. Without any, the one
  repository the targets name is the level.
- A repository must lie inside the targets. An organization needs `orgs/NAME` or no targets; `users/NAME` does not
  name it.
- The account level follows the account rules of [Users](users.md): with targets, the connection names the account
  as `users/NAME` and no repository, and a read of the account (`GET /user`) proves it before every request, reads
  included. Without targets, there is no proof read.
- A webhook ID is addressed inside the path of its owner, so a number of another owner never reaches a hook; no
  further proof read is needed at the repository and organization levels. An answer for another ID is refused.
- The Git hook and push mirror tools take a repository inside the targets.

## Webhooks

- `webhooks.list` and `webhooks.get` show the webhook object without `authorization_header`, `secret`, and
  `metadata`. Of `config` only `url`, `content_type`, and the Slack members `channel`, `username`, `icon_url`, and
  `color` appear; any other key is dropped, so a secret stored there never shows.
- An address with user info or a query is left out, because webhook URLs often carry a token.
- Gitea adds `name`; Forgejo adds `content_type` and `url` (the settings page of the webhook).
- Pausing or resuming a webhook is not offered, nor are creating and testing a webhook and system webhooks.
- `webhooks.delete` is listed-only, final, and sent as one request.

## Git hooks

- A script is untrusted text, cut at 8192 bytes (`content_truncated`), and never run. Hook names are lowercase words
  such as `pre-receive`.
- `git-hooks.delete` empties the hook and is listed-only. Changing a script is not offered.

## Push mirrors

- The target address is shown unless it carries user info or a query; it is never fetched. The remote username,
  password, and the public key text are never shown. Forgejo adds `branch_filter`; `last_error` is cut at 1024 bytes.
- `push-mirrors.sync` pushes to all existing mirrors of the repository and is an execution: it needs the `execute`
  effect and a confirmation, and is sent once without a repeat. The instance answers asynchronously.
- `push-mirrors.delete` is listed-only and final. Creating a push mirror is not offered.
