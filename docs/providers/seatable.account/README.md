---
description: >
  Describes the SeaTable account provider: its configuration, the one-base target, the snapshot list, and the
  confirmed restore into a new base outside every connection. Token and rights are in the setup page.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-10-26
---

# SeaTable account

This provider lists the snapshots of one SeaTable base and restores one of them. It uses the account-level
REST API, which needs an account token. The SeaTable provider, which works with one base's API token, needs
no account credential and is unaffected; the two are separate providers with separate connections.

To connect Qatlas, create the token, and see which rights it needs and how to fix errors, read
[Set up SeaTable account](setup.md).

## Configuration

The credential provides the secret `account-token`.

`base_url` is the SeaTable server, `https://cloud.seatable.io` by default or a self-hosted `https` origin,
without a path, user, query, or fragment. No redirect is followed.

```yaml
services:
  seatable-account:
    provider: seatable.account
    base_url: https://cloud.seatable.io

credentials:
  seatable-account-token:
    provider: seatable.account
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

### Choosing the base

The picker lists the bases of all workspaces of the account (`GET /api/v2.1/workspaces/`, which has no paging)
as `WORKSPACE_ID/BASE_NAME`, independent of the connection's target. Bases whose name is not a valid target
are left out. A base address of the web interface on the connection's own host,
`https://HOST/workspace/WORKSPACE_ID/dtable/BASE_NAME/`, resolves to that target without a request; other
hosts and `http` resolve to nothing.

## Tools

| Tool | Effect | Confirmation | Does |
| --- | --- | --- | --- |
| `seatable.account.snapshots.list` | read | none | lists the snapshots of the bound base, page by page |
| `seatable.account.snapshots.restore` | create | required | restores one snapshot into a new base |

The read profile offers `snapshots.list` only. `snapshots.restore` has no profile: a connection offers it
only when its `tools` list names it.

```yaml
connections:
  customer-a-restore:
    service: seatable-account
    credential: seatable-account-token
    target: 42/Customer base
    tools: [seatable.account.snapshots.list, seatable.account.snapshots.restore]
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
`invalid-provider-response`, `provider-error`). Qatlas' message never carries the provider's text or the token;
SeaTable's answer (the status and `error_msg`) travels apart from it in the `provider` field of the error detail,
marked untrusted, redacted, and bounded. The invoke log contains no arguments and no results. Causes and remedies by symptom are in [Fixing errors](setup.md#fixing-errors).

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
