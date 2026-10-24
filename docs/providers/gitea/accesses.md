---
description: >
  Describes the Gitea and Forgejo tools for existing accesses: reading and deleting webhooks of a repository, an
  organization, or the account behind the token, reading and resetting server-side Git hooks, reading, syncing, and
  deleting push mirrors, and reading and deleting deploy keys, SSH and GPG keys, access token metadata, and OAuth2
  applications. It covers the token scopes, the binding to the targets, who the instance serves, what is never
  shown, and why nothing creates or changes an access.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Accesses

These tools read what already reaches into or out of a repository, an organization, or the account: webhooks,
server-side Git hooks, push mirrors, deploy keys, SSH and GPG keys, access tokens, and OAuth2 applications. They
create nothing and change no target. Addresses, names, branch
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
| `deploy-keys.*` | `repository` | repository administrators |
| `user-keys.*`, `user-gpg-keys.*`, `oauth2-apps.*` | `user` | the account; Gitea: no public-only token |
| `user-keys.list`, `user-gpg-keys.list` with `username` | `user` | any reader the instance lets see the user |
| `access-tokens.list` (Forgejo) | `user` | the account itself |
| `access-tokens.get-current`, `.delete-current` (Gitea) | none | any valid token |

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
- The Git hook, push mirror, and deploy key tools take a repository inside the targets.
- The key, token, and OAuth2 application tools of the account follow the same account rules as the account level of
  webhooks: with targets, `users/NAME` and a read of the account before every request, no repository target. A key
  or application ID is addressed inside the path of the account, so no further proof is needed; a deploy key ID
  inside the path of its repository. An answer for another ID is refused.
- `access-tokens.list` always reads the account first, because the list is addressed by the login. It takes no
  user name: tokens of other people belong to the administration and are not read here.
- `user-keys.list` and `user-gpg-keys.list` take `username` for the public keys of another person. That reaches past
  any target, so only a connection without targets offers it; with targets it is refused before any request.

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

## Keys

- Lists and reads show the metadata and the fingerprint, never the key text: `key`, `public_key`, the key text of
  GPG subkeys, and the deploy token a Gitea 28 deploy key may carry are not in any answer. The fields are an
  allowlist; a field added later stays hidden until it is listed.
- `deploy-keys.list` and `user-keys.list` filter by `fingerprint`. A GPG key shows its IDs, capabilities, expiry,
  subkeys, and the email addresses it carries, which are personal data.
- `deploy-keys.delete`, `user-keys.delete`, and `user-gpg-keys.delete` are listed-only, final, and sent as one
  request. Adding or verifying a key is not offered, nor is requesting a GPG verification token.
- Gitea creates deploy tokens (`POST .../keys/tokens`); the API has no way to list or revoke them, so no tool
  reaches them.

## Access tokens

- A token's name, ID, scopes, and creation or last use are shown; `sha1` and `token_last_eight` never are, so neither
  the value nor a part of it leaves the instance. Forgejo adds the repositories a token is limited to.
- Forgejo has `access-tokens.list` for the own account. Gitea answers that list only to Basic Auth, which a
  connection never uses, so Gitea offers `access-tokens.get-current` for the token the connection sends instead.
  Forgejo has no such route.
- `access-tokens.delete-current` (Gitea) is listed-only and revokes the token the connection uses. The connection is
  unusable afterwards until its credential is replaced.
- Deleting a listed token by ID answers only Basic Auth on Forgejo, so it is not offered. Creating tokens is not
  offered either.

## OAuth2 applications

- The client secret is never shown. Redirect addresses are untrusted data, never fetched; an address with user info
  or a query is left out. Gitea adds `skip_secondary_authorization`.
- The API has no state to switch, so there is no pause; `oauth2-apps.delete` is listed-only and final.
  Creating and changing an application is not offered.
