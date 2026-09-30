---
description: >
  Describes the SeaTable account provider: the account token and how a person creates it, the one-base target,
  the snapshot list, the confirmed restore into a new base outside every connection, and the open risks of a
  broad account token.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-09-30
---

# SeaTable account

This provider lists the snapshots of one SeaTable base and restores one of them. It uses the account-level
REST API, which needs an account token. The SeaTable provider, which works with one base's API token, needs
no account credential and is unaffected; the two are separate providers with separate connections.

## Account token

The credential provides `account-token`. A person creates it; Qatlas never asks for or stores a password.
The person sends `POST /api2/auth-token/` to the SeaTable server with the account's username and password,
adding the one-time password where two-factor authentication is on, and stores the returned token in the
credential (`qatlas credential set`).

**The token is broad.** It acts as the person who created it and reaches everything that account reaches, far
beyond one base. Qatlas narrows what a connection exposes through its base target, its `tools` list, and its
`permissions`; it does not narrow what the token itself could do. Keep the credential out of connections that
do not need it, and revoke the token in SeaTable when it is no longer needed.

Open risk: accounts that sign in only through single sign-on may have no username and password to call
`POST /api2/auth-token/` with; how such an account obtains an account token has not been verified here.

`base_url` is the SeaTable server, `https://cloud.seatable.io` by default or a self-hosted `https` origin,
without a path, user, query, or fragment. No redirect is followed.

```yaml
services:
  seatable-account:
    provider: seatableaccount
    base_url: https://cloud.seatable.io

credentials:
  seatable-account-token:
    provider: seatableaccount
    type: keyring
```

## Scope

A connection is bound to exactly one base with the target `WORKSPACE_ID/BASE_NAME`: a positive integer
workspace ID, a slash, and the base name. The base name is one plain path segment, without a slash, a
backslash, or a control character. Both values are taken only from the target, never from an argument, and
are placed in the request path escaped.

```yaml
connections:
  customer-a-snapshots:
    service: seatable-account
    credential: seatable-account-token
    target: 42/Customer base
```

## Tools

| Tool | Effect | Confirmation | Does |
| --- | --- | --- | --- |
| `seatableaccount.snapshots.list` | read | none | lists the snapshots of the bound base, page by page |
| `seatableaccount.snapshots.restore` | create | required | restores one snapshot into a new base |

The read profile offers `snapshots.list` only. `snapshots.restore` has no profile: a connection offers it
only when its `tools` list names it and its `permissions` include `create`.

```yaml
connections:
  customer-a-restore:
    service: seatable-account
    credential: seatable-account-token
    target: 42/Customer base
    permissions: [read, create]
    tools: [seatableaccount.snapshots.list, seatableaccount.snapshots.restore]
```

`snapshots.list` takes `page` (from 1) and `per_page` (1 to 100; 50 when omitted) and answers `snapshots`
(`commit_id`, `base_name`, `created_at`), `page`, and `has_more`, which is SeaTable's own `has_next_page`.
Names and times are data of the provider and are not trusted; strings are cut at 512 bytes and a response
is read up to 1 MiB.

`snapshots.restore` takes only `commit_id`. Before it restores, it reads the bound base's own snapshot list
(up to 20 pages of 100) and refuses a `commit_id` that is not in it, without naming anything the provider
said; these reads change nothing. It then sends exactly one restore request and lets SeaTable name the new
base. It answers `restored` and the new base's `id`, `workspace_id`, and `name`.

**The restored base is a new base in the account, outside every connection.** The bound base stays
unchanged. No connection of Qatlas reaches the new base unless a person adds one for it, and repeating the
call creates another base. A failure that could mean the request nonetheless arrived (a timeout, a connection
reset, a 5xx, or an unreadable answer) is reported as uncertain and never retried by Qatlas: check the
SeaTable account for the new base before calling again.

## Errors

Errors carry a stable class (`auth`, `permission`, `not-found`, `rate-limited`, `timeout`, `unreachable`,
`invalid-provider-response`, `provider-error`) and never the provider's text or the token. The invoke log
contains no arguments and no results.

## API forms

Checked against the official API reference (api.seatable.com/reference) on 2026-09-30, not against a live
server:

- `GET /api/v2.1/workspace/{workspace_id}/dtable/{base_name}/snapshots/` with `page` and `per_page`,
  answering `snapshot_list` (`dtable_name`, `commit_id`, `ctime`) and `page_info` (`has_next_page`).
- `POST /api/v2.1/workspace/{workspace_id}/dtable/{base_name}/snapshots/{commit_id}/restore/`, with an
  optional `snapshot_name` that this provider does not send, answering `dtable` (`id`, `workspace_id`,
  `name`, and more).
- Authorization is `Bearer` with the account token.

Assumed, not documented: the shape of a `commit_id` (8 to 64 letters and digits is accepted), and that an
empty JSON object is accepted as the restore request's body.
