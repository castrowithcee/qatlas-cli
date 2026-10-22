---
description: >
  Describes the Make connection tools (stored third-party credentials, not Qatlas connections): the allow-listed
  reads, test, rename, delete, creating connections and setting their data from a forward credential, the access
  lists of locked connections, and the credential requests.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Make: connections and credential requests

Errors follow [Errors](README.md#errors).

## Connections

The connection tools address Make connections (stored third-party credentials), not Qatlas connections.
The `connections-read` profile (`make.connections.list`, `get`, `editable-schema`) needs `connections:read`.
`make.connections.test` (profile `connections-test`), `make.connections.rename` (profile `connections-manage`), the
access-list change, and the separately offered `delete`, `create`, and `set-data` additionally need
`connections:write`; `set-data` additionally needs `connections:read`, which binds the connection first.

`make.connections.list` always sends the bound team as `teamId` (and an optional `type[]` filter). Every tool
that names a `connection_id` validates it locally (positive integer), reads the connection from Make first,
and refuses it as an invalid request, without naming its real team, when its `teamId` is not the bound team
(or, with an organization target, its `organizationId` does not match or is not reported), before any schema or
test request is sent. A Qatlas connection with a scenario allow-list reaches no Make connections at all:
connections belong to no scenario, so the narrower reading is refusal, locally and before any secret is read.

Only an allow-list of fields leaves Qatlas: `id`, `name`, `accountName`, `accountType`, `packageName`,
`expire`, `scoped`, `teamId`, `organizationId`, `editable`. Make is also asked for exactly these through
`cols[]`, and the answer is decoded into a struct holding only them, so tokens, `metadata`, `data`, scope
lists, and any other field are dropped; strings are capped at 256 characters. Make's connection answer is not
documented field by field beyond property names, which is why an allow-list decides, not a deny-list.
`make.connections.editable-schema` reads `GET /connections/{id}/editable-data-schema`, which Make documents as
`editableParameters`, an array of strings; only those names are returned (at most 64, 128 characters each),
never a value or default.

`make.connections.test` is an `execute` with an outside effect: Make uses the stored credential against the
third-party service (`POST /connections/{id}/test`, `connections:write`, answer `{"verified":boolean}`). It is
`open_world`, needs its own confirmation, sends exactly one POST after the binding read, and is never retried;
a timeout, a connection reset, a 5xx, or an unreadable answer is reported as uncertain. The result is only
`verified`; Make reports no message. It sits in its own `connections-test` profile so a token or connection
can be granted reads without it. Everything a connection answers with is untrusted data.

`make.connections.rename` reads and binds the connection, sends one `PATCH /connections/{id}` with only
`name` (1 to 128 characters, no control characters), and reads the connection again, so the result carries the
same allow-listed fields and the same team binding as every read. It sits in the `connections-manage` profile.

`make.connections.delete` reads and binds the connection, then sends one `DELETE /connections/{id}`. Make
requires `confirmed=true` when a scenario includes the connection and otherwise refuses and deletes nothing.
Without the argument `confirm_scenarios_affected` the tool never sends `confirmed`: when Make refuses and its
answer names scenarios, the tool returns `confirmation_required` with those scenario ids and names (at most
20, names capped, untrusted data) and repeats nothing; a refusal that names no scenario is reported as an
ordinary error. With `confirm_scenarios_affected` true it sends `confirmed=true` and the scenarios using the
connection stop working. A connection that is not in any scenario is deleted by the first request. Because the
deletion is final, the tool is in no profile and is offered only when a connection's `tools` list names it.

`make.connection-access.list` (profile `connections-access-read`, `connections:read`) and
`make.connection-access.set` (profile `connections-access-manage`, `connections:write`, additionally `user:read`) use
Make's access-list endpoints
(`GET /teams/{teamId}/connections/{id}/access-list`, `POST .../access-list/users`, and
`PATCH .../access-list/users/{userId}`; the team is always the bound team and the connection is bound first).
Make answers them only when its locked connections feature is enabled for the organization (otherwise HTTP
400), and a change needs the entity manage permission of the token's user on the connection. The list returns
only user ids and roles, never a name or email, at most 200 members. `set` takes one `user_id` (already a
member of the bound team) and a `role` of `admin` or `member`: it first proves the team membership with
`GET /users/{userId}/user-team-roles/{teamId}` (`user:read`; a
missing or other-team role is refused as an invalid request without naming anything, before any change),
reads the list, sends nothing when the user
already holds the role, and otherwise sends exactly one `POST` (user not on the list) or `PATCH` (user on the
list with another role). It adds or changes that one member and removes nobody. The rest of the list is untouched
and no tool removes a member; Make itself refuses
a change that would leave the connection without an administrator. Because the list read may show only the
members visible to the token, a user it does not show can answer 409 on the `POST`, which is reported, never
retried. A change is not repeated after a timeout, reset, 5xx, or unreadable answer; the result is then
reported as uncertain.

### Creating connections and setting their data

`make.connections.create` (`POST /connections?teamId=`) and `make.connections.set-data`
(`POST /connections/{id}/set-data`) send secrets to Make. They are offered only when a connection's `tools` list
names them. A value is never an argument:
`secret_ref` names a forward credential that the Qatlas connection releases in `forward_secrets`; the core
checks it before the confirmation gate and refuses an unreleased or unknown name (`secret-ref-not-allowed`).
The values are read only after the confirmation and, for `set-data`, after the connection was read and bound
to the bound team, for the one request, registered with the redactor, and set into the request body under the
field names of the forward credential. A plain field with the same name as a secret field, or with a member
the tool sets itself, is refused, never overwritten. The plain `fields` argument is not redacted: put no
secret into it. Because of `secret_ref`, both tools need a tools list entry, the `create` or `update`
permission, and their own confirmation; they are in no profile. The secret role of the API token needs
`connections:write` for them (and `connections:read` for `set-data`).

- **Create.** `POST /connections?teamId=TEAM` with `accountName` (the `name`, 1 to 128 characters),
  `accountType` (`connection_type`, letters, digits, `.`, `_`, `:`, `-`, at most 100 characters), optional
  `scopes` (at most 50 entries of at most 256 characters), and the `fields` and secret fields as further
  top-level properties. `fields` is an object of at most 50 entries, names of letters, digits, `_`, `-`, `.`
  starting with a letter, values strings of at most 1024 characters, numbers, or booleans; `accountName`,
  `accountType`, `scopes`, `accountVisibility`, and `teamId` are not accepted as field names. The team is
  always the bound team, never an argument; a connection with a scenario allow-list refuses the tool locally.
  The result is the allow-listed connection metadata, with the team re-checked (a mismatch is a
  `provider-error`, the connection already exists). Locked visibility is not offered. A repeated call creates
  another connection. An OAuth type is created but not authorized: a person has to complete the authorization
  in Make's web interface.
- **Set data.** `POST /connections/{id}/set-data` with the `fields` and the secret fields. The connection is read
  first and refused, before any secret is read, unless Make reports it in the bound team (and organization,
  when bound), without naming any other team. **Make replaces the stored data**: a field that is in neither
  `fields` nor the referenced forward credential is set empty, a stored secret included, so give every plain
  field in `fields` and every secret field through `secret_ref`; `make.connections.editable-schema` lists the
  parameter names. At least one of `fields` and `secret_ref` is required. For an OAuth connection a person must
  afterwards log in to Make and confirm the change with the Reauthorize button; other types use the new data
  at once. Make refuses a connection it cannot edit ("Cannot edit this connection"). The result is
  `connection_id`, `team_id`, and `changed`.
- **One request.** Each tool sends exactly one changing request and never retries it: after a timeout, reset,
  5xx, or unreadable answer the outcome is reported as uncertain (list the connections before creating again,
  test the connection before setting its data again).

## Credential requests

A credential request asks a person to enter the secret of a connection or key themselves, through a link Make
serves; no secret value is accepted, sent, or returned by these tools. They sit in their own sensitivity class
(`make-credential-requests`) because the link is a way to enter secrets. A Qatlas connection with a scenario
allow-list reaches no credential requests (they belong to no scenario), refused locally before any secret is read.
The `credentialrequests-read` profile (`make.credential-requests.list`, `get`) needs `credential-requests:read`; its
`credentialrequests-manage` profile (plus `create`, which additionally needs `user:read`) and the separately offered
`make.credential-requests.delete` need `credential-requests:write`.

`list` always sends the bound team as `teamId` (optional `status` and `name` filters). `get` and `delete` take
a UUID `request_id`, read the request first, and refuse it as an invalid request, without naming its real team,
when its `teamId` (or, with an organization target, `organizationId`) is not the bound one, before any change.
Only an allow-list leaves Qatlas: `id`, `teamId`, `organizationId`, `name`, `description`, `status`,
`createdAt`, `updatedAt`, `expiresAt`, and the provider's `id` and `name`; never an email address, the link, or
any other field; strings are capped at 512 characters, at most 200 entries per list. Make documents the public
link only on the create answer, so `list` and `get` never return it.

`create` sends one `POST /credential-requests/requests/v2` (the deprecated endpoint is not used) with
typed, bounded fields only: `name` (1 to 255), optional `description` (at most 512), 1 to 16 `credentials`
(`app_name` as a Make app name, 1 to 32 `app_modules` names or `*`, optional integer `app_version`,
`name_override`, `description`), and the required person `provider_user_id`, whose membership in the bound team
is proven first with `GET /users/{userId}/user-team-roles/{teamId}` (`user:read`; otherwise refused without
naming anything). No new user is invited. The team is the
connection's own, never an argument. The answer is the request and `public_link` (https only, at most 2048
characters, untrusted, never followed); it is the only tool that returns the request link. A repeated call creates a
second request. A timeout, reset, 5xx, or
unreadable answer is reported as uncertain and not repeated.

`delete` sends one `DELETE /credential-requests/requests/{id}`. Without `confirm_credentials_deleted` it sends
no `confirmed` and the credentials already entered stay; with it true it sends `confirmed=true`, and Make also
deletes the connections and keys created from the request, so scenarios using them stop working. Because the
deletion is final, the tool is in no profile and is offered only when a connection's `tools` list names it.
