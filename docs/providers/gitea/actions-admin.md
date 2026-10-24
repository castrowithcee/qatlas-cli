---
description: >
  Describes the Actions administration tools of the Gitea and Forgejo providers: the secrets and variables of a
  repository, an organization, or the user behind the token, how the level is bound to the targets, how a secret
  value reaches the instance only by reference and never comes back, the listed-only tools, their scopes, and the
  product differences.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Actions administration

These tools manage what workflows run with: the Actions secrets and variables of a repository, an organization, or
the user behind the token. They hold for Gitea and Forgejo alike unless a line names one product. Reading and
operating runs is described in [Actions](actions.md).

## Scopes

A read presumably needs the `read:` scope and a change the `write:` scope of the level's category: `repository` for
a repository, `organization` for an organization, `user` for the user level. None was checked against a live
instance; a missing scope shows as `not-found`.

## Levels and binding

Every tool acts on one level: `repository` (`OWNER/REPO`) or `owner` (`orgs/NAME` or `users/NAME`), never both.
`repository` may be left out when `owner` is too and the targets name exactly one repository.

- The level is checked against the targets before the credential is resolved and before any request, and a
  refusal does not name it. The owner level needs the owner as an owner target of the same kind: a repository
  target does not allow it, `users/NAME` does not allow `orgs/NAME`, and a connection without targets reaches it.
- The API serves the user level only for the account behind the token. Qatlas therefore reads that account first
  and refuses another user in `owner` before anything is changed or a secret is read.
- Secrets and variables name no repository; the fixed path of the level binds them. A variable answered on the
  owner level that belongs to a repository is dropped from a list and refused by `actions-variables.get`, and an
  answer for another name is refused as `invalid-provider-response`.

## Secrets

A secret value is never an argument and never readable: the API answers names and metadata only, and
`actions-secrets.list` shows nothing else.

`actions-secrets.set` creates or replaces one secret. Its value comes from `secret_ref`, a forward credential with
exactly one field that the connection releases in `forward_secrets`. The core refuses an unreleased or unknown name
before the confirmation gate (`secret-ref-not-allowed`), and a credential with another number of fields is refused
before the token is resolved. The value is read only for the confirmed call, after the level was proven, registered
with the redactor, and sent once in the request body. The answer is discarded unread, so an instance that echoes the
body cannot hand the value on; the result names the secret and whether the instance created or replaced it. No
result, error, audit event, or invocation log entry carries the value.

Because of `secret_ref`, `actions-secrets.set` is offered only by a connection whose `tools` list names it, needs
the `update` permission and its own confirmation, and is in no setup profile.

```yaml
credentials:
  deploy-key:
    type: keyring
    forward: true
    fields: [value]

connections:
  forge-acme-secrets:
    service: forge
    credential: forge-token
    targets:
      - repos/acme/widget
    tools:
      - gitea.actions-secrets.list
      - gitea.actions-secrets.set
    forward_secrets:
      - deploy-key
```

## Variables

Variables are not secrets: `actions-variables.list` and `actions-variables.get` return their values, which are
untrusted data. A repeated `actions-variables.create` creates nothing, since the instance refuses a name that
exists. `actions-variables.update` replaces the value and renames the variable with `new_name`. Put no secret into a variable value; it is an ordinary argument and not redacted.

## Changes

Every change needs confirmation, sends exactly one request, and is never repeated; when the outcome is open, the
message says that the change may have been applied. `actions-secrets.delete` and `actions-variables.delete` remove
the secret or variable for good; they are offered only by a connection whose `tools` list names them and are in no
setup profile. No tool of this group is in a setup profile.

## Versions and products

Every operation exists from Forgejo 15 and Gitea 1.27, except the list of the secrets of a user: Forgejo serves it
from 17 and refuses it on an older instance before the request, naming the version; Gitea has none and refuses
`owner` as `users/NAME` for `actions-secrets.list` as `invalid-request`. Gitea keeps a `description` on secrets and
variables, and only Gitea's tools take one.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `actions-secrets.list`, `actions-variables.list`, `actions-variables.get` | read | both |
| `actions-variables.create` | create | both |
| `actions-variables.update` | update | both |
| `actions-secrets.set` | update, listed only | both |
| `actions-secrets.delete`, `actions-variables.delete` | delete, listed only | both |
