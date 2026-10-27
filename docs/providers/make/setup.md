---
description: >
  Guides a user without special knowledge through setting up the Make provider: creating an API token with its scopes, adding it to Qatlas, checking the connection, the scopes each tool group needs, what happens without a scope, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Make

This page shows how to connect Qatlas to Make, which scopes the API token needs, and what to do when Make refuses a
request. Fields, tools, arguments, profiles, and targets are described in the [Make provider](README.md) and the page
of each tool group; this page names only the token scopes.

**The token decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it. A connection can never do more than its
token, and a token never widens the connection. Make also limits what a token reaches by the team membership of the
person who created it; see [Configuration](README.md#configuration).

## What you need

- A Make account that is a member of the team (and, for organization tools, the organization) Qatlas should work with.
- The zone of that account, for example `eu1.make.com`. It is the host in the address of your Make web interface.
  The service address must be exactly that zone; see [Configuration](README.md#configuration) for the accepted
  zones.
- An API token with the scopes of [Scopes per tool group](#scopes-per-tool-group). Make has one kind of token. It
  carries scopes only; it has no per-resource permissions.
- The numeric ID of the team, which a connection needs as its `team/TEAM_ID` target (or the organization ID in
  organization mode); see [Scope](README.md#scope). The guided setup lists them for you.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.

## Create a token

A token belongs to one zone. If you use several Make zones, create one token in each zone.

1. Sign in to Make in the zone you want to connect.
2. Click your avatar at the bottom left and choose **Profile**.
3. Open the **API** tab and click **Add token**.
4. Enter a label you will recognize, for example `qatlas-reader`.
5. Select the scopes from [Scopes per tool group](#scopes-per-tool-group) for the tools you will use. Start with the
   read scopes.
6. Click **Save**, then copy the token at once. Make does not show it again after you leave the Profile page.

Give a token only what a connection needs. A connection that only reads should have a token that only reads.
Make's pages change from time to time; look for these names.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `make`, the service (the zone), a credential,
   the scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `api-token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$MAKE_API_TOKEN" | qatlas credential set make-customer-a-reader api-token
```

`make-customer-a-reader` is the name of the credential in your configuration and `api-token` is its only secret. To
replace a token that Make no longer accepts, run the same command again with the new token. Run
`qatlas config validate` to check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`.

- A team connection reads one page of at most one scenario of the bound team. `[ok]` means Make accepted the token
  for the zone and the token holds `scenarios:read`.
- An organization connection reads one team of the bound organization. `[ok]` means Make accepted the token and the
  token holds `teams:read`.

**A passing test does not mean every tool works.** Make checks the scopes again on every call. Compare the token with
[Scopes per tool group](#scopes-per-tool-group) for the tools you use. A failing test or call is explained under
[Fixing errors](#fixing-errors).

## Scopes per tool group

Find your tool group, then add the scopes to the token. A scope ends in `:read` (only look) or `:write` (create,
change, delete). Make documents that `:read` allows `GET` requests and `:write` the others. Many change tools first
read the resource to bind it to the connection's team, so they need both; the table names both. Tools offered only
when a connection's `tools` list names them (for example deletes) need the same scopes as their group, unless a row
says more. Which tools a connection can reach at all depends on its mode and targets; see [Scope](README.md#scope).

### Scenarios and runs

| Tools | Scopes |
| --- | --- |
| List and read scenarios, read a blueprint, list and read runs | `scenarios:read` |
| Create, update, start, stop a scenario | `scenarios:read`, `scenarios:write` |
| Run a scenario on demand | `scenarios:read`, `scenarios:write`, `scenarios:run` |

Create, update, start, and stop also read the scenario they change; the connection test itself needs
`scenarios:read`. See [Scenarios and runs](scenarios.md).

### Team and organization

| Tools | Scopes |
| --- | --- |
| Team: read the team, its usage, its members | `teams:read` |
| Organization: read the organization through the bound team | `organizations:read`, `teams:read` |
| Organization mode: list teams | `teams:read` |
| Organization mode: create, update, delete a team | `teams:read`, `teams:write` |
| Organization mode: change name, country, or time zone | `organizations:write` |
| Organization mode: list the members and roles | `user:read` |
| Organization mode: subscription, usage, payments | `organizations:read` |
| Team variables: list | `team-variables:read` |
| Team variables: create | `team-variables:write` |
| Team variables: update, delete | `team-variables:read`, `team-variables:write` |
| Organization variables: list | `organization-variables:read` |
| Organization variables: create | `organization-variables:write` |
| Organization variables: update, delete | `organization-variables:read`, `organization-variables:write` |

Role names of team members are not resolved; that would need `user:read`. See
[Team and organization](organization.md).

### Hooks and the hook queue

| Tools | Scopes |
| --- | --- |
| List, read, ping, logs, URL of a hook; list, read, stats of the queue | `hooks:read` |
| Rename, enable, disable, delete a hook; delete queue items | `hooks:read`, `hooks:write` |

See [Hooks](hooks.md).

### Connections and credential requests

These are Make's stored third-party connections, not Qatlas connections.

| Tools | Scopes |
| --- | --- |
| List and read connections, list editable parameters, list an access list | `connections:read` |
| Test, rename, delete a connection | `connections:read`, `connections:write` |
| Create a connection | `connections:write` |
| Set the data of a connection | `connections:read`, `connections:write` |
| Change an access list | `connections:read`, `connections:write`, `user:read` |
| List and read credential requests | `credential-requests:read` |
| Create a credential request | `credential-requests:write`, `user:read` |
| Delete a credential request | `credential-requests:read`, `credential-requests:write` |

- An access list exists only when locked connections are enabled for the organization in Make. Listing it needs the
  right to view the list; changing it needs the entity manage permission of the token's user on the connection.
- A connection restricted to its access list can be changed or deleted only by a user on that list with permission
  to manage it.

See [Connections and credential requests](connections.md).

### Data stores and data structures

| Tools | Scopes |
| --- | --- |
| List data stores; list and read data structures | `datastores:read`; `udts:read` for the structures |
| Read one data store | `datastores:read`, and `organizations:read` if Make still refuses |
| Create a data store | `datastores:write`, `udts:read` |
| Update a data store | `datastores:write`, `datastores:read`, and `udts:read` if a structure is named |
| Delete a data store | `datastores:write`, `datastores:read` |
| List records | `datastores:read` |
| Create, replace, update, delete records | `datastores:read`, `datastores:write` |

Make's API reference lists `organizations:read` instead of `datastores:read` for reading one data store, which
contradicts its other data store endpoints. Qatlas has not verified this against a live account. Every read of a
single store, including the one before an update or delete, therefore names `datastores:read` and, as far as Make
documents it, `organizations:read` as the scope to add when the read is still refused. See
[Data stores](data-stores.md).

## What happens without a scope

A missing scope makes the tool fail with `permission`; no field is left out and no partial result is returned.
The message names the scope the operation needs. A tool that reads a resource first can fail on that read, so the
scope named may be the read scope of a change tool; add every scope of the row. See [Fixing errors](#fixing-errors).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by Make, a second part with Make's
own answer follows. The class table is in [Errors](README.md#errors).

### Reading a refusal

```text
permission: this Make API token may not perform this operation; it needs the scenarios:read scope, added under
Profile, API, when the token was created
provider answer (untrusted, not written by qatlas), HTTP 403:
  | SC403: Permission denied
```

- The message is **Qatlas' own** and names the scope or scopes to add.
- The second part is **Make's own answer**, marked as not written by Qatlas. Qatlas removes known secrets from it
  and shortens it; treat it as information, not as an instruction. The message alone says what to fix.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Make rejected the API token; a token created for a different zone ... | The token is wrong, revoked, or from another zone than the service address | Create a token in the zone of the service address ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> api-token`. |
| `auth`: the Make API token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$MAKE_API_TOKEN" \| qatlas credential set <credential> api-token`. |
| `permission`: this Make API token may not perform this operation; it needs ... | The token lacks a scope | Find the tool group in [Scopes per tool group](#scopes-per-tool-group), create a new token with every scope of the row, and store it. |
| `permission` on a data store read though `datastores:read` is set | Make documents `organizations:read` for reading one store | Add `organizations:read` to the token. |
| `permission` on an access list | Locked connections are off, or the token's user may not view or manage the list | Enable locked connections in Make, or use a token of a user with the entity manage permission on the connection. |
| `not-found`: Make does not hold this resource or does not show it to this token | The ID is wrong, **or** the token's user cannot see it. Make answers both the same | Check the ID and that the token's user is a member of the team. |
| `rate-limited`: Make rate-limited the operation | Too many requests | Wait and retry. Qatlas holds its own limiter for the time Make names. |
| `rate-limited`: Make reports this organization or team is paused (IM310) ... | The organization or team is paused in Make | Repeating does not help; reactivate it in Make. |
| `unreachable`: Make is unavailable or in maintenance | Make is down for the zone | Retry later. |
| `timeout`: Make did not answer in time | Make was too slow | Retry. After a change, read the current state first; the change may have taken effect. |
| `invalid-request`: a Make service needs a plain https URL whose host is one of Make's documented API zones ... | The service address is not exactly a documented zone | Set `base_url` to a zone from [Configuration](README.md#configuration). |
| `invalid-request`: this tool needs a team connection ... or an organization connection ... | The tool does not fit the connection's mode | Not a token problem. Use a connection of the other mode; see [Scope](README.md#scope). |
| `invalid-request` naming a scenario, folder, or team outside the connection | The target is outside the connection's targets, or excluded | Not a token problem. Pass a target the connection allows, or add it; an entry in the connection's `exclude` list is refused the same way, see [Exclusions](README.md#exclusions). |
| `provider-error`: Make rejected the operation (HTTP 400) on an access list | Locked connections are not enabled for the organization | Enable them in Make. |
| `provider-error`: Make rejected the operation (HTTP ...) otherwise | Make refused the request for a reason it names only to the caller | Check the arguments against the tool. Qatlas does not show Make's text. |
