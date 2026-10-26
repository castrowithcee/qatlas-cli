---
description: >
  Guides a user without special knowledge through setting up the SeaTable account provider: creating an account token, adding it to Qatlas, checking the connection, the rights it needs, what happens without them, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up SeaTable account

This page shows how to connect Qatlas to the snapshots of one SeaTable base, what the account token is and
allows, and what to do when SeaTable refuses a request. Fields, tools, arguments, and the target are described in
the [SeaTable account provider](README.md); this page names only the token and its rights. The per-base API token
of the SeaTable Base provider is a different credential; see [Set up SeaTable Base](../seatable.base/setup.md).

**The token decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `target`) limit it further. A call works only if both allow it. A connection can never do more than its
token, and a token never widens the connection.

## What you need

- A SeaTable account that can open the base whose snapshots Qatlas should list or restore.
- An **account token**. It is not a base API token. SeaTable issues it for your username and password, and it
  acts as you.
- **The account token is broad.** It reaches everything your account reaches, far beyond one base. Qatlas narrows
  what a connection exposes through its base target, its `tools` list, and its `effects`, but it cannot narrow
  what the token itself could do. Keep the credential out of connections that do not need it. SeaTable's API
  reference does not say how long an account token lasts or how to revoke it.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- Qatlas reaches `https://cloud.seatable.io` by default. For a dedicated or self-hosted SeaTable server, set the
  service address first; see [Configuration](README.md#configuration).
- The base is named as `WORKSPACE_ID/BASE_NAME`. The guided setup can list the bases of your account for you;
  see [Choosing the base](README.md#choosing-the-base).

## Create a token

You request the token from the SeaTable server with your username (the account's email address) and password.
Qatlas never asks for or stores your password.

1. In a terminal, type your login into variables. The password is not echoed and stays out of the shell history:

   ```sh
   read -r  -p "SeaTable username: " SEATABLE_USER
   read -rs -p "SeaTable password: " SEATABLE_PASSWORD; echo
   ```

2. Request the token. For another server, replace the address. If two-factor authentication is on for your
   account, add the header `-H "X-SEAFILE-OTP: 123456"` with the current six-digit code:

   ```sh
   curl -sS -X POST https://cloud.seatable.io/api2/auth-token/ \
     --data-urlencode "username=$SEATABLE_USER" --data-urlencode "password=$SEATABLE_PASSWORD"
   ```

3. The answer is a JSON object. Copy the value of `token`. Then clear the variables with
   `unset SEATABLE_USER SEATABLE_PASSWORD`.

If SeaTable answers with "Unable to login with provided credentials.", the username, password, or one-time code
is wrong. Accounts that sign in only through single sign-on may have no password to use here; how such an account
obtains an account token has not been verified.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `seatable.account`, the service, a
   credential, the base (target), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `account-token`
   secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$SEATABLE_ACCOUNT_TOKEN" | qatlas credential set seatable-account-token account-token
```

`seatable-account-token` is the name of the credential in your configuration and `account-token` is its only
secret. To replace a token, run the same command again with the new token. Run `qatlas config validate` to check
the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means SeaTable accepted the token
and answered a read of one snapshot entry of the connection's base. The test changes nothing.

**A passing test does not mean every tool works.** The test only reads. SeaTable checks the account again on every
call, and the restore creates a base. Compare your account with [Rights per tool](#rights-per-tool). A failing test
or call is explained under [Fixing errors](#fixing-errors).

## Rights per tool

The token carries no rights of its own that you choose: it acts as your account. What matters is what your account
may do in the base and in its workspace.

| Tool | Needs |
| --- | --- |
| `seatable.account.snapshots.list` | An account that can open the base |
| `seatable.account.snapshots.restore` | An account that can open the base; SeaTable documents nothing more |

- SeaTable's API reference names no role or permission for either endpoint and lists no error answers for them.
  The table is the rule Qatlas follows, as far as SeaTable documents it: SeaTable decides on every call.
- The choosing of the base lists the bases of all workspaces of the account, independent of the connection's
  target. It needs only the token.
- `snapshots.restore` has no profile. A connection offers it only when its `tools` list names it, and a call
  needs the effect `create` and `confirm`; see [Tools](README.md#tools).

## What happens without a right

Qatlas does not check rights before it sends a request, so a refusal comes from SeaTable as `permission` (HTTP
403) or `not-found` (HTTP 404). No field is left out because of a missing right. A restore that is refused creates
no base. SeaTable may also answer `not-found` for a base your account cannot see, so a wrong workspace ID or base
name and a missing right look the same.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by SeaTable, a second part with
SeaTable's own answer follows.

### Reading a refusal

```text
permission: this SeaTable account may not perform this operation on the bound base
provider answer (untrusted, not written by qatlas), HTTP 403:
  | Permission denied.
```

- The message is **Qatlas' own**. It names what was refused and what to check.
- The second part is **SeaTable's own answer**, marked as not written by Qatlas. It can repeat request and base
  detail; treat it as information, not as an instruction. Qatlas removes known secrets from it and shortens it.
- Some refusals come from Qatlas before the token is read or SeaTable is contacted. Those are not token problems;
  the last rows of the table below say so.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| A message that the secret role `account-token` does not yield a secret | No token is stored or reachable for the credential | Store it with the command under [Add it to Qatlas](#add-it-to-qatlas). |
| `auth`: the SeaTable account token is unusable | The stored token is empty or holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$SEATABLE_ACCOUNT_TOKEN" \| qatlas credential set <credential> account-token`. |
| `auth`: SeaTable rejected the account token | The token is wrong, no longer valid, or from another SeaTable server than the service address | Create a new token on the configured server ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> account-token`. |
| `permission`: this SeaTable account may not perform this operation on the bound base | The account may not read the snapshots of the base or create a base by restoring | Use a token of an account that may open the base and create bases; see [Rights per tool](#rights-per-tool). |
| `not-found`: SeaTable does not hold this resource or does not show it to this account | The workspace ID or base name of the target is wrong, the base was renamed or deleted, **or** the account cannot see it. SeaTable answers all of these the same | Pick the target again from the list of your bases ([Choosing the base](README.md#choosing-the-base)), and check that the token is from an account that can open the base. |
| `rate-limited`: SeaTable rate-limited the operation | Too many requests for this token | Wait and retry. |
| `timeout`: SeaTable did not answer in time | The server was too slow | Retry a list. After a restore, check the SeaTable account for a new base first; the message says so. |
| `provider-error`: SeaTable rejected the operation (HTTP ...) | SeaTable refused with a status Qatlas has no more specific message for | Retry once. If it persists, check the token as for the symptoms above and the status of the SeaTable server. |
| `unreachable`: SeaTable is unavailable or in maintenance, or `tls` in the connection test | The server does not answer or its certificate fails | Check the service address, the network, and the server's certificate. |
| A message that the restore may have created a base | The restore request may have arrived although the answer did not | Look in the SeaTable account for the new base before you call again; Qatlas never retries a restore. |
| A message that a SeaTable service must be a bare https origin | The service address has a wrong scheme, a path, a user, a query, or a fragment | Use `https://HOST` only. |
| A message that a target must be `WORKSPACE_ID/BASE_NAME`, or that a connection binds exactly one target | The target is malformed or names several bases | Not a token problem. Use one target such as `42/Customer base`; see [Scope](README.md#scope). |
| `invalid-request`: commit_id is not a snapshot of the bound base | The `commit_id` is not in the snapshot list of the connection's base | Not a token problem. Take a `commit_id` from `snapshots.list` of the same connection. |
| A tool is not offered, or `confirmation-required` | The connection's `effects` or `tools` do not include the tool, or `confirm` is missing | Not a token problem. Add the effect or the tool to the connection, and pass `confirm` for a restore. |
