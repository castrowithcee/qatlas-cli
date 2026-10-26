---
description: >
  Guides a user without special knowledge through setting up the Forgejo provider: creating an access token, adding it to Qatlas, checking the connection, the token rights of the tools only Forgejo has, and fixing errors by symptom. Everything Forgejo shares with Gitea is on the Gitea setup page.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Forgejo

This page shows what differs from [Set up Gitea](../gitea/setup.md) when you connect Qatlas to a Forgejo instance.
Forgejo uses the same scopes per tool group, the same steps to store a token, and the same errors; this page does
not repeat them. Fields, tools, and targets are in the [Forgejo provider](README.md) and the
[Gitea provider](../gitea/README.md).

**The token decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it.

## Create a token

In Forgejo, open your account menu, then **Settings**, then **Applications**, and generate a new token under the
access tokens. Forgejo changes its pages from time to time; look for these names.

1. Name it, set an expiry if offered, and set the permission of each category from
   [Rights per tool group](../gitea/setup.md#rights-per-tool-group) on the Gitea page. Those scopes hold for Forgejo
   as well.
2. **Select at least one scope**, even to read public repositories. Forgejo refuses a token without a scope.
3. From Forgejo 15, the page may offer to **bind the token to repositories**. Such a token reaches only those
   repositories and only the categories `repository` and `issue`; the other tools then fail. It is an additional
   limit: the targets of the connection stay the boundary.
4. The page may also offer a token for **public resources only**. It sees public data only; do not choose it for
   private data.
5. Generate the token and copy it at once. Forgejo shows it only once.

A repository-bound token and a public-only token answer HTTP 404 instead of 403 for what they do not reach. Qatlas
reports it as `not-found`, like every refusal.

## Add it to Qatlas

Follow [Add it to Qatlas](../gitea/setup.md#add-it-to-qatlas), with provider `forgejo` for the service and
`$FORGEJO_TOKEN` in place of the token variable:

```sh
printf %s "$FORGEJO_TOKEN" | qatlas credential set <credential> token
```

## Check the connection

Run the test as described in [Check the connection](../gitea/setup.md#check-the-connection). `[ok]` means Forgejo is
a supported version (15 or later) and accepted the token.

Qatlas recognizes Forgejo by `GET /api/forgejo/v1/version`. If a proxy in front of the instance blocks that path,
the instance is taken for Gitea and refused for a `forgejo` connection: the test fails with `provider-error`: the
instance does not identify as Forgejo; check the base URL and the provider of the service. Let the proxy pass that
path, or ask the operator to.

## Rights of the tools only Forgejo has

All other tools need the scopes of [Rights per tool group](../gitea/setup.md#rights-per-tool-group). That includes
the review comments and Git notes (`read:repository`, `write:repository` to change).

None of these scopes was checked against a live instance. They follow the route group of the Forgejo API that
serves each tool, so treat them as likely, not certain.

### Quota and quota administration

| Tools | Scope |
| --- | --- |
| `quota.*` of the account | `read:user` |
| `quota.*` of an organization (`owner`) | `read:organization`; the instance serves it to the organization's owners |
| `admin-quota-*`: read | `read:admin` |
| `admin-quota-*`: change | `write:admin` |

The `admin-quota-*` tools need a **site administrator** and a connection without targets. Use a token of its own
for them that no other connection uses.

### Tokens of the account

`access-tokens.list` needs `read:user` and is served to the account itself. `access-tokens.get-current` and
`access-tokens.delete-current` do not exist on Forgejo.

## Fixing errors

The errors, their codes, and their causes are the ones of
[Fixing errors](../gitea/setup.md#fixing-errors), with Forgejo in place of Gitea in the messages.

### Reading a refusal

The message begins "Forgejo does not show this resource to this token ..." and is followed by the provider answer
(status, `message`, and the documentation link) exactly as described in
[Reading a refusal](../gitea/setup.md#reading-a-refusal).

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `not-found`: Forgejo does not show this resource to this token ... | As on Gitea, **or** the token is bound to repositories and the resource is outside them, or the token is limited to public resources and the resource is private | Check the name and the scope as on Gitea. Then check the token's binding and public-only limit; create a token without them. |
| `not-found` for a call that worked before | A repository-bound token reaches only the categories `repository` and `issue` | Use a token without the binding for other tools. |
| `provider-error`: the instance does not identify as Forgejo; check the base URL and the provider of the service | The address belongs to Gitea or to something else, or a proxy blocks `/api/forgejo/v1/version` | Correct the base URL or the provider of the service, or let the proxy pass that path. |
| `provider-error`: the instance runs a Forgejo version older than 15.0.0, which Qatlas does not support | The instance is older than the supported version | Update the instance; see [Versions](README.md#versions). |
| `provider-error`: the quota is not available on this instance; it is turned off or not served | The instance has the quota switched off, or the routes are not served | Ask the administrator to turn the quota on. Not a token problem. |
| `not-found` for a quota tool | The organization or token is not reached, or the account is no owner of the organization | Check the `owner` and the scope above; for `admin-quota-*` use a site administrator with `read:admin` / `write:admin`. |
