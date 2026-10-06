---
description: >
  Describes the Make provider: zone and API token setup, the required team target plus the optional
  organization and scenario allow-lists and the live scenario-team check, the scenario and blueprint
  reads, the run (Make's own "logs") reads and their offset pagination, the bounded best-effort run error,
  the confirmed scenario create/update/start/stop/run tools and their contracts, and the scope and plan
  boundaries of a Make API token, and the argument-free reads of the bound team, its usage and members,
  and its organization, and the connection reads and test.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-06
---

# Make

Make is a provider for one Make (make.com) zone's REST API (`/api/v2`). It lists and reads scenarios, reads
a scenario's blueprint, lists and reads its run history, and, with confirmation, creates a scenario, replaces
a scenario's blueprint, scheduling, name, or folder, starts and stops it, and runs it on demand. Separate
read-only profiles read the bound team and its organization.

**There is deliberately no tool to delete, clone, or replay a scenario, and no generic webhook call.** A
Make scenario, once created, cannot be removed again by this provider; deleting one still requires the Make
UI or a direct API call outside Qatlas.

## Configuration

`base_url` is the Make zone this connection reaches: `https://eu1.make.com`, `https://eu2.make.com`,
`https://us1.make.com`, `https://us2.make.com`, `https://eu1.make.celonis.com`, or
`https://us1.make.celonis.com`, the zones Make documents as of this writing. It must be exactly one of
these hosts, plain `https`, without a port, path, user, query, or fragment; Qatlas refuses any other URL
before a secret is read, and never falls back to a free-form origin the way a self-hosted provider's base
URL can be.

The credential provides `api-token`, a Make API token created under the profile avatar, Profile, API, Add
token. The read profile needs `scenarios:read`; the manage profile's create, update, start, and stop tools
additionally need `scenarios:write`, and its run tool additionally needs `scenarios:run`. The `team` profile
(`make.team.get`, `make.team.usage`, `make.team.members`) needs `teams:read`; the `organization` profile
(`make.organization.get`) needs `organizations:read` and `teams:read`, because the organization is found
through the bound team; the `hooks-read` profile (`make.hooks.list`, `get`, `ping`, `logs`) needs
`hooks:read`, as does `make.hooks.url`; the `hooks-manage` profile (`make.hooks.create`, `rename`, `enable`,
`disable`, plus the hooks-read tools) additionally needs `hooks:write`, as does the separately offered
`make.hooks.delete`; the `hookqueue-read` profile (`make.hookqueue.list`, `get`, `stats`) needs `hooks:read`, and
the separately offered `make.hookqueue.delete` needs `hooks:write` (with `hooks:read`, which binds the hook); the `connections-read` profile (`make.connections.list`, `get`,
`editableschema`) needs `connections:read`, and its separate `connections-test` profile
(`make.connections.test`) additionally needs `connections:write`, as do the `connections-manage` profile
(`make.connections.rename`), the `connections-access-manage` profile (`make.connectionaccess.set`, which additionally needs `user:read`), and the
separately offered `make.connections.delete`, `make.connections.create`, and `make.connections.setdata`
(`setdata` additionally needs `connections:read`, which binds the connection first); the `connections-access-read` profile
(`make.connectionaccess.list`) needs `connections:read`; the `credentialrequests-read` profile
(`make.credentialrequests.list`, `get`) needs `credential-requests:read`, its `credentialrequests-manage`
profile (plus `create`, which additionally needs `user:read`) and the
separately offered `make.credentialrequests.delete` need `credential-requests:write`. A 403 names the missing scope. The `org-admin` profile
(`make.teams.list`, `create`, `update`, organization mode only) needs `teams:read` and `teams:write`, as does the
separately offered `make.teams.delete`. A Make token
belongs to exactly one zone: Make's own guidance is to create a separate token for each zone a person has
access to, so a token created for a different zone than this connection's own is rejected here the same way
as any other invalid token. The token carries API scopes, while Make also limits resources by the user's
team membership. This connection's required team target adds a Qatlas boundary within that access. Make
also documents optional locked connections with their own access lists; this feature must be enabled for
the organization; this provider enforces none of them itself and only reads and sets their members through
the access-list tools. See Make's [Connections API
reference](https://developers.make.com/api-documentation/api-reference/connections.md) and [connection
access-list reference](https://developers.make.com/api-documentation/api-reference/connections/access-list.md).

```yaml
services:
  make-customer-a:
    provider: make
    base_url: https://eu1.make.com

credentials:
  make-customer-a-reader:
    provider: make
    type: keyring
```

## Scope

A connection binds one zone, through its base URL and API token, and:

| Target | Binds |
| --- | --- |
| `team/TEAM_ID` | the one Make team this connection may reach; team mode: required, exactly one |
| `organization/ORG_ID` | team mode: the organization the bound team belongs to, optional, at most one; organization mode: required, exactly one |
| `scenario/SCENARIO_ID` | one scenario of the bound team; optional, repeatable; team mode only |

A connection has exactly one of two modes. **Team mode** (the default described here): exactly one `team/TEAM_ID`,
an optional `organization/ORG_ID`, and optional scenarios. **Organization mode**: no team and exactly one
`organization/ORG_ID`; scenario targets are refused (the narrower reading, since a scenario belongs to a team).
Configuration errors never quote a configured value. In organization mode every team tool (all tools except
`make.teams.*`, including `make.team.*` and `make.organization.get`) is refused as an invalid request ("needs a
team connection") before a secret is read or a request is sent; in team mode the `make.teams.*` tools are
refused the same way ("needs an organization connection"). The connection test of an organization connection
reads one team of the organization (`GET /teams`, `teams:read`).

```yaml
connections:
  customer-a-team-x:
    service: make-customer-a
    credential: make-customer-a-reader
    targets: [team/123456, organization/7890]
```

Team is **required**, unlike the optional allow-lists the n8n and Infomaniak providers offer. The target
limits this provider to one team; it does not replace Make's own API scopes, user team membership, or any
locked-connection access list that the organization has enabled and configured.

A `scenario_id` argument outside a configured scenario allow-list is refused locally, as an invalid request,
before any request is sent. A scenario's team membership is checked live, against Make's own answer, never
against local configuration alone: every tool that names a scenario_id directly (`make.scenarios.get`,
`make.scenarios.blueprint`, `make.scenarios.update`, `make.scenarios.start`, `make.scenarios.stop`,
`make.scenarios.run`, `make.runs.list`, and `make.runs.get`) reads the named scenario from Make first and
confirms its reported `teamId` matches the bound team before any content is read or any changing request is
sent. A scenario of another team is refused the same way as one outside the allow-list, and the refusal
never names the scenario's real team.

A scenario object itself reports no `organizationId`, only `teamId`, so a configured organization target
cannot be checked against a scenario directly. The live scenario check confirms only the bound team.
`make.runs.list` and `make.runs.get` can also check organization: a run's own answer carries both `teamId`
and `organizationId`, and both are defensively re-applied to every run this provider returns. The team and
organization tools check it against the bound team's own answer as well.

`make.scenarios.create` cannot name a scenario_id at all: it always creates in the connection's own bound
team, never a caller-supplied one, and it is refused outright on a connection restricted by a scenario
allow-list, since a scenario that does not exist yet can never already be on that list.
`make.scenarios.create`, `make.scenarios.update`, `make.scenarios.start`, and `make.scenarios.stop` all
re-read the scenario after their one changing request and re-apply the bound team and scenario allow-list to
it; a mismatch is reported as a `provider-error`, not an invalid request, because the change has already
happened and this provider has no delete tool to undo it. `make.scenarios.run` is never followed by a
re-read: its own answer carries no scope of its own to re-verify (see "Runs and their errors" below).

## Tools

| Tool | Effect | Confirmation | Does |
| --- | --- | --- | --- |
| `make.scenarios.list` | read | none | lists the scenarios of the bound team, filtered to the scenario allow-list, page by page |
| `make.scenarios.get` | read | none | reads one scenario, with its team membership confirmed live |
| `make.scenarios.blueprint` | read | none | reads one scenario's blueprint (modules, wiring, configuration) |
| `make.scenarios.create` | create | required | creates one scenario in the bound team from a blueprint and scheduling |
| `make.scenarios.update` | update | required | replaces a scenario's name, blueprint, scheduling, or folder; partial, only the fields given |
| `make.scenarios.start` | update | required | turns a scenario's scheduling on; Make says it also runs the scenario when it is scheduled at regular intervals |
| `make.scenarios.stop` | update | required | turns a scenario's scheduling off |
| `make.scenarios.run` | execute | required | starts exactly one new execution of a scenario on demand |
| `make.runs.list` | read | none | lists one scenario's run history, page by page |
| `make.runs.get` | read | none | reads one run's status, timings, operations, data volume, and a bounded best-effort error |
| `make.team.get` | read | none | reads the bound team's name, organization, pause state, limits, and consumption; no arguments |
| `make.team.usage` | read | none | reads the bound team's daily usage records (at most 31); no arguments |
| `make.team.members` | read | none | lists the bound team's members as user id and role id from `GET /teams/{id}/user-team-roles` (at most 200), without names or email addresses; no arguments |
| `make.organization.get` | read | none | reads id, zone, and flat scalar license limits of the bound team's organization; no arguments |
| `make.hooks.list` | read | none | lists the webhooks and mailhooks of the bound team (always `teamId` of the connection), page by page |
| `make.hooks.get` | read | none | reads one hook's state and queue, with its team confirmed live; never the trigger URL |
| `make.hooks.ping` | read | none | reads one hook's status (`GET /hooks/{id}/ping`: attached, learning, gone); sends no data to the hook |
| `make.hooks.logs` | read | none | lists one hook's log entries as metadata only (id, status, time, sizes) |
| `make.hooks.url` | read | none | returns one hook's trigger URL or mailhook address, a secret; offered only when a connection's `tools` list names it, in no profile |
| `make.hooks.create` | create | required | creates a webhook or mailhook in the bound team (`POST /hooks`); refused on a connection with a scenario allow-list; never returns the trigger URL |
| `make.hooks.rename` | update | required | renames one hook of the bound team (`PATCH /hooks/{id}`, body `name`) |
| `make.hooks.enable` / `make.hooks.disable` | update | required | enables or disables one hook (`POST /hooks/{id}/enable` or `/disable`), then re-reads it |
| `make.hooks.delete` | delete | required | deletes one hook (`DELETE /hooks/{id}`); offered only when a connection's `tools` list names it, in no profile |
| `make.hookqueue.list` | read | none | lists a hook's waiting incoming items as metadata only (`GET /hooks/{id}/incomings`: id, scope, size, time), never payloads |
| `make.hookqueue.get` | read | none | reads one waiting item (`GET /hooks/{id}/incomings/{incomingId}`) with a capped, untrusted payload and a `truncated` flag |
| `make.hookqueue.stats` | read | none | reads the queue count, capacity, and enabled state (`GET /hooks/{id}/incomings/stats`) |
| `make.hookqueue.delete` | delete | required | deletes explicitly named items (`DELETE /hooks/{id}/incomings`, body `ids` only); offered only when a connection's `tools` list names it, in no profile |
| `make.connections.list` | read | none | lists the bound team's Make connections (always `teamId` of the connection) by allow-listed metadata, at most 200 |
| `make.connections.get` | read | none | reads one Make connection by allow-listed metadata, with its team confirmed live |
| `make.connections.editableschema` | read | none | lists the names of the parameters Make allows to be edited on one connection; names only |
| `make.connections.test` | execute | required | asks Make to verify one connection's stored credential against its third-party service; one request, never retried |
| `make.connections.rename` | update | required | renames one connection of the bound team (`PATCH /connections/{id}`, name up to 128 characters), then reads it again |
| `make.connections.create` | create | required | creates one connection in the bound team from plain fields and a `secret_ref` (`POST /connections?teamId=`); only offered by a tools list |
| `make.connections.setdata` | update | required | replaces the data of one connection of the bound team from plain fields and a `secret_ref` (`POST /connections/{id}/set-data`); OAuth connections need a reauthorization in Make; only offered by a tools list |
| `make.connections.delete` | delete | required | deletes one connection for good; without `confirm_scenarios_affected` it sends no `confirmed` and returns the scenarios Make names when it refuses; offered only through a tools list |
| `make.connectionaccess.list` | read | none | lists the users and roles on a locked connection's access list; ids and roles only |
| `make.connectionaccess.set` | update | required | gives one team user the role `admin` or `member` on a locked connection's access list; adds or changes that one member, removes nobody |
| `make.datastores.list` | read | none | lists the bound team's data stores (always `teamId` of the connection), sorted by name, page by page; never records |
| `make.datastores.get` | read | none | reads one data store, with its team confirmed live; never records |
| `make.datastores.create` | create | required | creates one data store in the bound team (`POST /data-stores`) after reading that its data structure belongs to the team |
| `make.datastores.update` | update | required | changes a data store's name, data structure, or maximum size (`PATCH /data-stores/{id}`), after reading the store and any named structure |
| `make.datastores.delete` | delete | required | deletes one data store with its records (`DELETE /data-stores`, body `ids` with exactly one id); without `confirm_scenarios_affected` it sends no `confirmed`; offered only through a tools list |
| `make.datastorerecords.list` | read | none | lists records (key and capped data) of one data store of the bound team after binding the store; at most 50 per page, each record capped; untrusted personal data |
| `make.datastorerecords.create` | create | required | creates one record (`POST /data-stores/{id}/data`, optional `key` and a bounded `data` object) after binding the store |
| `make.datastorerecords.delete` | delete | required | deletes explicitly named records (`DELETE /data-stores/{id}/data`, body `keys`, 1 to 50 distinct); never `all`, `exceptKeys`, or `confirmed`; offered only through a tools list |
| `make.teamvariables.list` | read | none | lists the bound team's custom and system variables (name, type, capped value, `is_system`); values may be secret |
| `make.teamvariables.create` | create | required | creates one custom variable in the bound team (`POST /teams/{teamId}/variables`) |
| `make.teamvariables.update` | update | required | sets type and value of one existing custom variable (`PATCH /teams/{teamId}/variables/{name}`) after listing it; never a system variable |
| `make.teamvariables.delete` | delete | required | deletes one custom variable (`DELETE /teams/{teamId}/variables/{name}?confirmed=true`) after listing it, only with `confirmed: true`; offered only through a tools list |
| `make.datastructures.list` | read | none | lists the bound team's data structures (id, name, strict), sorted by name, page by page |
| `make.datastructures.get` | read | none | reads one data structure with its bounded field specification, with its team confirmed live |
| `make.credentialrequests.list` | read | none | lists the bound team's credential requests by allow-listed metadata, at most 200; never a link or email |
| `make.credentialrequests.get` | read | none | reads one credential request, with its team confirmed live; never the link or an email |
| `make.credentialrequests.create` | create | required | creates one credential request in the bound team (`POST /credential-requests/requests/v2`); the only tool that returns the request link |
| `make.credentialrequests.delete` | delete | required | deletes one credential request; without `confirm_credentials_deleted` it sends no `confirmed`; offered only through a tools list |

Every read is safe and needs no confirmation. Every change of the manage profile needs its own confirmation,
sends exactly one changing request, and is never retried by this provider itself: a failure that could mean
the request nonetheless reached Make (a timeout, a connection reset, or a 5xx) is reported as uncertain
instead, naming what to check before trying again.

The four team and organization tools take no arguments and never accept a team or organization ID. The team
is the connection's own target; `make.organization.get` takes the `organizationId` from the bound team's own
answer. A configured `organization/ID` target is checked against that answer, and a mismatch is refused as an
invalid request that never names the other organization. Organization details are limited to id, zone, and
the license object's flat scalar values (nested values are dropped, at most 64 entries). Role names are not
resolved, because that needs the separate `user:read` scope.

## Teams of an organization

`make.teams.list`, `.create`, `.update`, and `.delete` work on the teams of the bound organization, in
organization mode only. None takes an organization argument; update and delete take `team_id`, which is read
live (`GET /teams/{teamId}`) and bound to the connection's organization through its `organizationId` before
anything changes. A team of another organization, and a personal space, is refused as an invalid request
without naming it, and no changing request is sent.

| Tool | Request | Scope |
| --- | --- | --- |
| `make.teams.list` | `GET /teams?organizationId=` (up to 200, personal spaces left out) | `teams:read` |
| `make.teams.create` | `POST /teams` with `name` (1 to 128), `organizationId`, optional `operations_limit` (0 to 2,000,000,000) | `teams:write` |
| `make.teams.update` | `PATCH /teams/{teamId}` with `name` and/or `operations_limit` | `teams:write`, `teams:read` |
| `make.teams.delete` | `DELETE /teams/{teamId}?confirmed=true` | `teams:write`, `teams:read` |

Changes need their own confirmation, send exactly one changing request, and are never retried; a timeout,
a connection reset, a 5xx, or an unreadable answer is reported as uncertain. **Deleting a team also deletes all of
its data: scenarios, webhooks, and custom team variables.** Qatlas does not look them up. `confirmed: true` is a
required argument and the only way Make's `confirmed` flag is sent. The tool is in no profile and is offered only
when a connection's `tools` list names it. The profile `org-admin` holds list, create, and update. Team names
are untrusted data.

## Organization master data

`make.organization.update` (organization mode only) changes the bound organization's `name` (1 to 128 characters:
letters, numbers, spaces, and `' - . ( ) * + , @ _ /`), `country_id`, or `timezone_id` with one
`PATCH /organizations/{organizationId}` (`organizations:write`). The organization is always the connection's
target; plan, license, and payment fields are not offered. The answer must report the bound organization,
otherwise the result is uncertain. It needs its own confirmation, sends exactly one request, is never retried, and
is in the `org-admin` profile. Inviting people and listing members with their organization role are not offered:
the API reference documents no member list with organization roles and no fixed role IDs for the invitation.

## Pagination

Both list tools take `offset` and `limit` (1 to 200; 50 when omitted) and answer `offset`, `count`, and
`has_more`. Make's own pagination object (`pg`) echoes the request's offset and limit but reports no total
count, so `has_more` is a heuristic, not a value Make states: it is true exactly when a page came back full,
the same convention the SeaTable provider's own offset pagination uses. `make.scenarios.list` also accepts
`is_active` and `name`, forwarded to Make's own filters of the same kind. `make.runs.list` also accepts
`status` (`1` success, `2` warning, `3` error, exactly as Make reports it) and `from_ms`/`to_ms`
(Unix epoch milliseconds). Every returned page is still defensively re-filtered against this connection's
own team, and, for runs, organization, after Make answers.

## Blueprints and connection references

`make.scenarios.blueprint` reads one scenario's blueprint and passes it through unmodified beyond this
provider's own response size limit. A module's `parameters` may reference a connection, a key, or a webhook,
but only ever by the numeric id Make itself assigns it, never by the value stored behind that id: this is
exactly why a blueprint can be exported and re-imported into another scenario without ever re-entering a
credential, and why Make's own "Clone scenario" endpoint accepts an id-to-id `account`/`key`/`hook` mapping
rather than any secret. This provider's own scope check still runs first: the scenario is read and its team
membership confirmed before the blueprint itself is ever requested, since a blueprint answer carries no
`teamId` of its own to check.

`make.scenarios.create` and `make.scenarios.update` accept `blueprint` and `scheduling` as ordinary JSON
objects, up to 4 MiB and 8 KiB respectively, nested no deeper than 64 and 8 levels; both limits are this
provider's own local ceiling, not one Make documents. Make's own OpenAPI schema declares both fields as
request-body **strings**, not nested objects
(developers.make.com/api-documentation/api-reference/scenarios): this provider encodes each one to a compact
JSON string exactly once before it is placed in the request body, so a caller never has to double-encode
anything itself. This encoding was checked through this package's own documentation-query tooling during
this session (2026-09-27), not against a live create or update call; verify it against a real zone before
relying on it in a new setting. `scenarios.run`'s own `data` argument is documented differently, as a plain
nested object, and is sent as one, unencoded (see "Runs and their errors" below).

`scheduling` must be a JSON object with at least a `type` field (a string, 1 to 64 characters); Make
documents `type` and `interval` but no exhaustive list of valid `type` values or their further, type-specific
fields (for example a specific weekday or time), so this provider does not enumerate or restrict them beyond
that: it lets Make's own create and update validate them. A module's connection, key, or webhook reference
inside a blueprint is validated by Make itself, the same as a read; this provider does not and cannot check
whether a referenced connection actually exists or belongs to the bound team.

`make.scenarios.create` sends `teamId` as the connection's own bound team on every call; it is never an
argument a caller can set. `make.scenarios.update` never moves a scenario between teams: Make's own PATCH
does not accept a `teamId` field at all.

## Starting, stopping, and folders

`make.scenarios.start` and `make.scenarios.stop` each send Make's own `POST /scenarios/{id}/start` or
`/stop`, then re-read the scenario to confirm the active state actually changed. Repeating either keeps the
requested active state: start leaves an already active scenario active, and stop leaves an inactive scenario
inactive. This describes the state result, not every side effect. Make documents that activating a scenario
also runs it if it is scheduled at regular intervals ([scenario API reference](https://developers.make.com/api-documentation/api-reference/scenarios.md)). The reference does not say whether calling `/start` on an already active, regularly scheduled scenario triggers another run. Treat that effect as unverified and do not infer that repeating start is safe to retry. `make.scenarios.run` independently requests an on-demand run.

`make.scenarios.update`'s `folder_id` moves a scenario into a folder; `clear_folder` removes its folder
assignment instead (Make's own PATCH accepts `folderId: null` for that). The two are mutually exclusive, and
`make.scenarios.update` refuses a call that gives neither `folder_id`, `clear_folder`, `name`, `blueprint`,
nor `scheduling`: there would be nothing left to change.

## Runs and their errors

`make.runs.get` reads one run's status, timings, operations, and data volume from Make's own log entry, and,
only when that entry's `detail` object happens to carry the recognised `detail.error.message` shape, a short
excerpt of it, bounded to 2048 characters. Make does not document the `detail` object's shape field by
field, so this extraction is deliberately conservative: anything else `detail` may carry, including the
versioned scenario-history change Make attaches to a "modify"-type log entry, is never surfaced, never
passed through raw, and never guessed at. A run never returns a bundle's actual input or output data, which
this endpoint does not expose in the first place.

`make.scenarios.run` starts exactly one new execution of a scenario on demand (`POST
/scenarios/{id}/run`), needing the `scenarios:read`, `scenarios:write`, and `scenarios:run` scopes together.
Its `data` argument, when given, is the run's input parameters as a JSON object, up to 64 KiB, nested no
deeper than 16 levels; unlike `blueprint` and `scheduling` above, Make documents `data` as a plain nested
object, so this provider sends it as one, unencoded. `callback_url` is never offered as an argument at all:
accepting a caller-chosen URL here would place it directly into an outbound webhook call this provider does
not control, the same reasoning no provider in this codebase offers a generic webhook call.

`responsive` defaults to **false**, even though Make's own default is also false: this provider is explicit
about it because setting it true makes the one run request block until the scenario finishes, which could
easily outlast this provider's own request timeout for a long-running scenario and then be reported as
merely uncertain when the run in fact started successfully and needs no repeating.

`make.scenarios.run` answers with `execution_id` and, when Make reports one immediately, `status`; `status`
is passed through opaquely and untyped, since Make's own documentation was not internally consistent about
its shape during this session's research (a numeric code in one place, a string elsewhere). Use
`make.runs.get` or `make.runs.list` afterward to learn a run's outcome: Make documents a run's own
`executionId` identically, word for word, as the identifier a scenario's log entry reports under its own
`id` key, for both its "Get execution log" and its separate "Get scenario execution details" endpoints, which
is the basis for pointing a caller at `make.runs.get` (built on the logs endpoint this provider already
implements) rather than at that separate, undocumented-here `executions` endpoint, which this provider does
not add a tool for. Whether a still-running execution is already visible through the logs endpoint before it
finishes is not documented either way and is not assumed; this is a known gap, not a confirmed guarantee.

Unlike every other change of this provider, `make.scenarios.run` is never followed by a re-read: its own
answer carries no `teamId` or `organizationId` of its own for this provider to re-verify. A timeout, a
connection reset, or a 5xx after the one run request is reported with a note that names `make.runs.list`
specifically, not "the current state" of a resource this provider would otherwise show directly.

Running a scenario that is currently inactive (its scheduling turned off) is not specially handled: Make's
exact error shape for that case was not confirmed during this session's research, so it surfaces through
this provider's ordinary status classification below rather than a fabricated, scenario-specific one.

## Errors

Errors keep stable classes and never carry the API token or a raw provider response body:

| Class | Cause |
| --- | --- |
| `auth` | Make rejected the API token, including a token created for a different zone than this connection's own |
| `permission` | this API token may not perform the operation; the message names the exact scope(s) needed: `scenarios:read` for a read, `scenarios:write` for create/update/start/stop, or `scenarios:read`, `scenarios:write`, and `scenarios:run` together for a run; `hooks:read` for the hook reads and `hooks:write` (with `hooks:read`) for the hook changes; `connections:read` for the connection reads and `connections:write` for the connection test, rename, delete, create, set-data, and access-list change; `user:read` for the user's team membership check of the access-list change and of a credential request's `provider_user_id`; `credential-requests:read` for the credential request reads and `credential-requests:write` for their create and delete; `datastores:read` and `datastores:write` for the data store and record tools (the single-store read also names `organizations:read`, see "Data stores and data structures"), `team-variables:read` and `team-variables:write` (with `team-variables:read`) for the team variable tools, and `udts:read` for the data structure tools and the structure check of a store create or update, `teams:read` and `teams:write` (with `teams:read`) for the organization-mode team tools, `organizations:write` for `make.organization.update`; the access-list tools additionally name Make's own right to view or manage the list (locked connections enabled for the organization, entity manage for a change) |
| `not-found` | Make does not hold the resource or does not show it to this token |
| `rate-limited` | Make rate-limited the request; Qatlas applies no proactive spacing of its own and instead holds its own limiter for whatever `Retry-After` Make names. A 429 whose body names Make's own `IM310` code is named distinctly as a paused organization or team, not a transient limit: repeating the request will not help until it is reactivated |
| `timeout` | Make did not answer in time |
| `unreachable` | Make is unavailable, in maintenance, or could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, named a different resource than the one requested, or, for a change, did not confirm the change actually took effect |
| `provider-error` | every other rejection, including a redirect on an endpoint that must not answer with one, and a create, update, start, or stop whose re-read result fell outside this connection's own team or scenario allow-list after the one changing request already reached Make |

A `scenario_id` outside the connection's allow-list, and a scenario whose live team check reports a team
outside the bound team, are invalid requests, never provider errors. A configured organization target is
checked against run entries, not scenario details. A scope refusal is never mistaken for a missing scenario
or run; the same is true of an unconfirmed call to any change tool, and of a
`make.scenarios.update` call that gives nothing to change or gives both `folder_id` and `clear_folder`.

## Hooks

The five read tools need the `hooks:read` scope (a 403 names it). `make.hooks.list` always sends the bound
team as `teamId`. Every tool that names a `hook_id` validates it locally (positive integer), reads the hook
from Make first, and refuses it as an invalid request, without naming its real team, when its `teamId` is not
the bound team, before any ping, log, or URL request is sent. On a connection with a scenario allow-list the
hook tools are narrower than the scenario tools: only hooks assigned to a listed scenario are listed or
reachable, and an unassigned hook is not.

The trigger URL, a mailhook address, the hook's `udid`, and `ping.address` are trigger secrets: anyone who
knows them can start the scenario. `list`, `get`, `ping`, and `logs` omit them and mask any part of them inside
the strings they return. Only `make.hooks.url` returns the URL; it is not part of any profile and needs a
connection whose `tools` list names it. Its value is deliberately not added to the output redactor, since that
would blank the tool's own answer. Hook logs are reduced to metadata (id, status, time, replayable flag, type,
and at most eight integer sizes); request headers, bodies, parsers, and udids are never read into a result.

### Hook queue

The queue tools are named `make.hookqueue.*` because operation IDs have exactly three segments. Each reads the
hook first and refuses a hook of another team, or outside a scenario allow-list, before any queue request.
Queued payloads are untrusted and may hold personal data: they carry the data sensitivity
`make-webhook-payloads-personal-data`. `list` returns metadata only. `get` returns the payload capped to
6 levels, 200 values, 256-byte strings, and 8 KiB, with `truncated` set when anything was cut or the whole
payload had to be dropped; the hook's trigger URL, `udid`, and mailhook address are masked inside it, and the
values of `authorization`, `cookie`, `set-cookie`, `host`, `referer`, and `origin` keys are replaced. Incoming
ids must be 1 to 64 letters, digits, underscores, or hyphens, checked before any secret or request.

`make.hookqueue.delete` takes 1 to 50 distinct explicit `ids`, needs its own confirmation, sends exactly one
`DELETE` with only `ids` (never `all`, `exceptIds`, or Make's `confirmed` flag, which Make requires only for
deleting everything), and is never repeated. After a 5xx, a dropped connection, an unreadable answer, or an
error in the answer it reports that items may have been deleted; the queue should be read before trying again.

### Changing hooks

`make.hooks.create`, `rename`, `enable`, `disable`, and `delete` need `hooks:write` (a 403 names it) and
`hooks:read`, which binds the hook. Each needs its own confirmation, sends exactly one changing request, and
is never repeated: a 5xx, a dropped connection, or an unreadable answer is reported as uncertain, and the
current state should be read before trying again. Every tool that names a `hook_id` reads the hook first and
refuses it, before the changing request, unless its `teamId` is the bound team (and, with a scenario
allow-list, its scenario is listed). Enable and disable re-read the hook and report an unconfirmed state as
uncertain; a result outside the bound team after the change is a provider error.

`make.hooks.create` always sends the bound team as `teamId` (Make documents it as a string) together with
`name`, `typeName`, `method`, `headers`, and `stringify`, all required by Make. Narrower than Make: `type` is
only `webhook` (`gateway-webhook`) or `mailhook` (`gateway-mailhook`); `include_method`, `include_headers`,
and `stringify` are booleans for webhooks only; there is no connection, free type name, header, or data
field. Hooks of those two types need no connection (`__IMTCONN__` belongs to connection-bound types, which
this tool does not create). On a connection with a scenario allow-list the create is refused, since a new
hook is assigned to no scenario and the hook tools could never reach it. The answer never carries the
trigger URL, udid, or address; they are added to the output redactor.

`make.hooks.delete` sends `confirmed=true` only when `confirm_scenarios_affected` is true. Without it, Make
refuses the deletion of a hook a scenario uses; Qatlas then answers `deleted: false`,
`confirmation_required: true`, and up to 20 scenarios (ids, and names bounded and masked, untrusted) taken
from the refusal body when it lists any and from the hook read, and does not repeat the request. Make
documents only that an error is returned; its exact shape is not specified, so a refusal is recognized as a
4xx answer when scenarios are known, and any other failure stays an error.

## Connections

The connection tools address Make connections (stored third-party credentials), not Qatlas connections.
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
`make.connections.editableschema` reads `GET /connections/{id}/editable-data-schema`, which Make documents as
`editableParameters`, an array of strings; only those names are returned (at most 64, 128 characters each),
never a value or default. Updating a connection's data is not offered.

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

`make.connectionaccess.list` and `make.connectionaccess.set` use Make's access-list endpoints
(`GET /teams/{teamId}/connections/{id}/access-list`, `POST .../access-list/users`, and
`PATCH .../access-list/users/{userId}`; the team is always the bound team and the connection is bound first).
Make answers them only when its locked connections feature is enabled for the organization (otherwise HTTP
400), and a change needs the entity manage permission of the token's user on the connection. The list returns
only user ids and roles, never a name or email, at most 200 members. `set` takes one `user_id` (already a
member of the bound team) and a `role` of `admin` or `member`: it first proves the team membership with `GET /users/{userId}/user-team-roles/{teamId}` (`user:read`; a
missing or other-team role is refused as an invalid request without naming anything, before any change), reads
the list, sends nothing when the user
already holds the role, and otherwise sends exactly one `POST` (user not on the list) or `PATCH` (user on the
list with another role). The rest of the list is untouched and no tool removes a member; Make itself refuses
a change that would leave the connection without an administrator. Because the list read may show only the
members visible to the token, a user it does not show can answer 409 on the `POST`, which is reported, never
retried. A change is not repeated after a timeout, reset, 5xx, or unreadable answer; the result is then
reported as uncertain. Both tools are named `connectionaccess` because tool ids have three segments.

### Creating connections and setting their data

`make.connections.create` and `make.connections.setdata` send secrets to Make. A value is never an argument:
`secret_ref` names a forward credential that the Qatlas connection releases in `forward_secrets`; the core
checks it before the confirmation gate and refuses an unreleased or unknown name (`secret-ref-not-allowed`).
The values are read only after the confirmation and, for `setdata`, after the connection was read and bound
to the bound team, for the one request, registered with the redactor, and set into the request body under the
field names of the forward credential. A plain field with the same name as a secret field, or with a member
the tool sets itself, is refused, never overwritten. The plain `fields` argument is not redacted: put no
secret into it. Because of `secret_ref`, both tools need a tools list entry, the `create` or `update`
permission, and their own confirmation; they are in no profile. The secret role of the API token needs
`connections:write` for them (and `connections:read` for `setdata`).

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
  field in `fields` and every secret field through `secret_ref`; `make.connections.editableschema` lists the
  parameter names. At least one of `fields` and `secret_ref` is required. For an OAuth connection a person must
  afterwards log in to Make and confirm the change with the Reauthorize button; other types use the new data
  at once. Make refuses a connection it cannot edit ("Cannot edit this connection"). The result is
  `connection_id`, `team_id`, and `changed`.
- **One request.** Each tool sends exactly one changing request and never retries it: after a timeout, reset,
  5xx, or unreadable answer the outcome is reported as uncertain (list the connections before creating again,
  test the connection before setting its data again).

## Data stores and data structures

`make.datastores.*` manage the data stores (tables scenarios keep data in) of the bound team; the records inside
a store are not touched. `make.datastructures.list` and `make.datastructures.get` read the field layouts stores
follow; creating, changing, or deleting a structure is not offered. A Qatlas connection with a scenario
allow-list reaches none of them: they belong to no scenario, so the narrower reading is refusal, locally and
before any secret is read.

| Tool | Request | Make scope |
| --- | --- | --- |
| `make.datastores.list` | `GET /data-stores?teamId=` (`cols[]`, `pg[offset]`, `pg[limit]`, sorted by name) | `datastores:read` |
| `make.datastores.get` | `GET /data-stores/{id}` | `datastores:read` (see below) |
| `make.datastores.create` | `POST /data-stores` with `name`, `teamId`, `datastructureId`, `maxSizeMB` | `datastores:write`, plus `udts:read` for the structure check |
| `make.datastores.update` | `PATCH /data-stores/{id}` with the given fields of `name`, `datastructureId`, `maxSizeMB` | `datastores:write`, `datastores:read`, plus `udts:read` when a structure is named |
| `make.datastores.delete` | `DELETE /data-stores?teamId=[&confirmed=true]` with body `{"ids":[id]}` | `datastores:write`, `datastores:read` |
| `make.datastructures.list` | `GET /data-structures?teamId=` | `udts:read` |
| `make.datastructures.get` | `GET /data-structures/{id}` | `udts:read` |

**Scope contradiction in Make's reference.** The reference lists `organizations:read` as the scope of
`GET /data-stores/{id}` while every other data store endpoint, including the list, lists `datastores:read`. This
is most likely a documentation error. Every read of a single store (the `get` tool and the binding read before
an update or delete) therefore names `datastores:read` and, in the same 403 message, `organizations:read` as the
scope to add when `datastores:read` is present and the read is still refused. Not verified against a live
account.

Team binding. The teamId of list and create is always the connection's bound team, never an argument. Every
`datastore_id` and `datastructure_id` is read from Make first and refused as an invalid request, without naming
its real team, when its `teamId` is not the bound team; this happens before any change, so a `datastructure_id`
of another team in a create or update sends no changing request. Data stores and structures report no
organization, so a configured organization target is not checked against them.

Fields are typed and bounded: `name` 1 to 128 characters without control characters (it need not be unique),
`max_size_mb` an integer from 1 to 1048576 (the upper bound is a local ceiling, Make documents none), and an
update needs at least one of `name`, `datastructure_id`, `max_size_mb`. Only allow-listed fields are returned
(`id`, `name`, `team_id`, `records`, `size`, `max_size`, `datastructure_id`; strings capped at 256 characters;
size values are decoded as text or number). A structure's specification is returned as name, type, label,
required, multiline, sequence, codepage, and nested spec, at most 200 fields and 4 levels, 128 characters per
string, without default values; `spec_truncated` marks a cut. All of it is untrusted data. Lists return at most
200 entries per page.

`make.datastores.delete` reads and binds the store, then sends one `DELETE /data-stores` with the bound `teamId`
and exactly one id; the delete-all form is never used. Make requires `confirmed=true` when a scenario includes
the store and otherwise refuses and deletes nothing. Without `confirm_scenarios_affected` the tool never sends
`confirmed`: when Make refuses and names scenarios, it returns `confirmation_required` with those ids and names
(at most 20, names capped) and repeats nothing; a refusal naming no scenario is an ordinary error. With
`confirm_scenarios_affected` true it sends `confirmed=true`, the store is deleted with its records, and the
scenarios using it stop working. The tool is in no profile and is offered only when a connection's `tools` list
names it. The profiles are `datastores-read`, `datastores-manage` (reads plus create and update), and
`datastructures-read`. Create, update, and delete need their own confirmation, send exactly one changing
request after the reads, and are never retried; a timeout, a connection reset, a 5xx, or an unreadable answer is
reported as uncertain, and a result outside the bound team after the change as a `provider-error`.

## Data store records

`make.datastorerecords.list`, `.create`, and `.delete` work on the records of one data store of the bound team.
Tool ids have three segments, so the object is named `datastorerecords`. Every `datastore_id` is read and bound
to the bound team first (see above), before the list or the one changing request; a store of another team is
refused without naming it. A connection with a scenario allow-list reaches none of them.

| Tool | Request | Make scope |
| --- | --- | --- |
| `make.datastorerecords.list` | `GET /data-stores/{id}/data` (`pg[offset]`, `pg[limit]`) | `datastores:read` |
| `make.datastorerecords.create` | `POST /data-stores/{id}/data` with `data` and optional `key` | `datastores:write`, `datastores:read` |
| `make.datastorerecords.delete` | `DELETE /data-stores/{id}/data` with body `{"keys":[...]}` | `datastores:write`, `datastores:read` |

Source: developers.make.com API reference, Data stores, Data (checked against the published reference, not a
live account). Record contents are untrusted user data and likely personal data (sensitivity
`make-data-store-records-personal-data`). A page holds at most 50 records (20 by default); each record's data is
capped to 8 KiB, 6 levels, 200 nodes, and 256 characters per string, with `truncated` set when anything was cut.
`create` takes `data` as a JSON object of at most 64 KiB and 16 levels (a local ceiling; Make validates it
against the store's data structure) and an optional `key` of 1 to 128 characters (letters, digits, `_`, `.`,
`:`, `-`, not starting with a dot or hyphen; Make documents no key format, so this is the narrower local form);
without a key Make generates one. `delete` takes 1 to 50 distinct keys of the same form and sends only the
`keys` form: Make's `all`, `exceptKeys`, and `confirmed` are never sent. It reports the requested keys Make
names as deleted. It is in no profile and is offered only when a connection's `tools` list names it. The profiles
are `datastorerecords-read` and `datastorerecords-manage` (list and create). Changes need their own confirmation,
send exactly one changing request, and are never retried; a timeout, a connection reset, a 5xx, or an unreadable
answer is reported as uncertain.

Replacing (`PUT /data-stores/{id}/data/{key}`) and updating (`PATCH`) a single record are not offered: the
reference documents their body only as having "no predefined body properties" and publishes no request example,
so whether the body is the record data itself or a `{"data":...}` wrapper is undocumented.

## Team variables

`make.teamvariables.list`, `.create`, `.update`, and `.delete` work on the variables of the bound team. Tool ids
have three segments, so the object is named `teamvariables`. None takes a team argument: the team is always the
connection's own target. A connection with a scenario allow-list reaches none of them, refused before any secret
is read. Organization variables are in the next section.

| Tool | Request | Make scope |
| --- | --- | --- |
| `make.teamvariables.list` | `GET /teams/{teamId}/variables` | `team-variables:read` |
| `make.teamvariables.create` | `POST /teams/{teamId}/variables` with `typeId`, `name`, `value` | `team-variables:write` |
| `make.teamvariables.update` | `PATCH /teams/{teamId}/variables/{name}` with `typeId`, `value` | `team-variables:write`, `team-variables:read` |
| `make.teamvariables.delete` | `DELETE /teams/{teamId}/variables/{name}?confirmed=true` | `team-variables:write`, `team-variables:read` |

Source: developers.make.com API reference, Teams, team variables (checked 2026-10-05 against the published
reference, not a live account). Types are `number` (1), `text` (2, Make's string), `boolean` (3), and `date`
(4, ISO 8601; only an RFC 3339 timestamp or `YYYY-MM-DD` is accepted, an assumption). A name has 1 to 128
letters, digits, `$`, or `_` (Make documents the characters, not the length); a text value at most 4096 bytes;
`null` is refused. Values may hold secrets or personal data (sensitivity `make-team-variables-may-hold-secrets`)
and are untrusted; a listed value is capped like other payloads, with `truncated` set when anything was cut, at
most 500 variables per list.

Without the `customVariables` license (see `make.organization.get`) Make reports only its system variables and
answers a delete with 404. The list therefore reports `custom_count` and `system_count`: `custom_count` 0 can mean
that the feature is unavailable. `update` and `delete` first list the variables and refuse a system variable or an
unknown name as an invalid request without sending the change; the reference does not say whether Make itself
would refuse a system variable. Renaming and the history endpoint are not offered, and `update` always sends
type and value together, as the reference asks.

`delete` needs the argument `confirmed: true`, which is what sets Make's `confirmed=true`; the reference states
that the call fails without it. Scenarios that read the variable by name lose its value, and Qatlas does not look
them up, so check them first. It is in no profile and is offered only when a connection's `tools` list names it.
The profiles are `teamvariables-read` and `teamvariables-manage` (list, create, update); neither is recommended.
Changes need their own confirmation, send exactly one changing request, and are never retried; a timeout, a
connection reset, a 5xx, or an unreadable answer is reported as uncertain.

## Organization variables

`make.organizationvariables.list`, `.create`, `.update`, and `.delete` work on the variables of the organization
a connection in organization mode is bound to; a team connection is refused before any secret is read. None
takes an organization argument. They behave like the team variables: same names, types, caps, validation, system
variable refusal, and uncertain-result handling.

| Tool | Request | Make scope |
| --- | --- | --- |
| `make.organizationvariables.list` | `GET /organizations/{organizationId}/variables` | `organization-variables:read` |
| `make.organizationvariables.create` | `POST /organizations/{organizationId}/variables` with `typeId`, `name`, `value` | `organization-variables:write` |
| `make.organizationvariables.update` | `PATCH /organizations/{organizationId}/variables/{name}` with `typeId`, `value` | `organization-variables:write`, `organization-variables:read` |
| `make.organizationvariables.delete` | `DELETE /organizations/{organizationId}/variables/{name}?confirmed=true` | `organization-variables:write`, `organization-variables:read` |

Source: developers.make.com API reference, Organizations (checked 2026-10-06 against the published reference,
not a live account). The list answer is read as an array under `organizationVariables`, an assumption from the
team variables. `delete` needs `confirmed: true`, is in no profile, and is offered only when a connection's
`tools` list names it. The profiles are `organizationvariables-read` and `organizationvariables-manage` (list,
create, update); neither is recommended. A 403 names `organization-variables:read` or `:write`. Sensitivity is
`make-organization-variables-may-hold-secrets`; values are untrusted.

## Credential requests

A credential request asks a person to enter the secret of a connection or key themselves, through a link Make
serves; no secret value is accepted, sent, or returned by these tools. They sit in their own sensitivity class
(`make-credential-requests`) because the link is a way to enter secrets. Tool ids have three segments, so the
object is named `credentialrequests`. A Qatlas connection with a scenario allow-list reaches no credential
requests (they belong to no scenario), refused locally before any secret is read.

`list` always sends the bound team as `teamId` (optional `status` and `name` filters). `get` and `delete` take
a UUID `request_id`, read the request first, and refuse it as an invalid request, without naming its real team,
when its `teamId` (or, with an organization target, `organizationId`) is not the bound one, before any change.
Only an allow-list leaves Qatlas: `id`, `teamId`, `organizationId`, `name`, `description`, `status`,
`createdAt`, `updatedAt`, `expiresAt`, and the provider's `id` and `name`; never an email address, the link, or
any other field; strings are capped at 512 characters. Make documents the public link only on the create
answer, so `list` and `get` never return it.

`create` sends one `POST /credential-requests/requests/v2` (the deprecated endpoint is not used) with
typed, bounded fields only: `name` (1 to 255), optional `description` (at most 512), 1 to 16 `credentials`
(`app_name` as a Make app name, 1 to 32 `app_modules` names or `*`, optional integer `app_version`,
`name_override`, `description`), and the required person `provider_user_id`, whose membership in the bound team
is proven first with `GET /users/{userId}/user-team-roles/{teamId}` (`user:read`; otherwise refused without
naming anything). No new user is invited. The team is the
connection's own, never an argument. The answer is the request and `public_link` (https only, at most 2048
characters, untrusted, never followed). A repeated call creates a second request. A timeout, reset, 5xx, or
unreadable answer is reported as uncertain and not repeated.

`delete` sends one `DELETE /credential-requests/requests/{id}`. Without `confirm_credentials_deleted` it sends
no `confirmed` and the credentials already entered stay; with it true it sends `confirmed=true`, and Make also
deletes the connections and keys created from the request, so scenarios using them stop working. Because the
deletion is final, the tool is in no profile and is offered only when a connection's `tools` list names it.

## Untrusted data

Scenario names, descriptions, scheduling configuration, blueprint content, run status, and every other value
a listing, a read, or a change answers with come from the zone and are untrusted data, whether this provider
read it or a caller supplied it for a create, update, or run. Qatlas normalises it into a stable envelope and
never renders it, follows a link inside it, or executes anything derived from it.

## Boundary

This provider lists and reads scenarios and runs, reads a scenario's blueprint, creates a scenario, replaces
a scenario's blueprint, scheduling, name, or folder, starts and stops a scenario, and runs one on demand. It
deliberately does not, and has no tool to, delete or clone a scenario, replay a run, make a generic webhook
call, start or stop a hook's learning mode, set a hook's data, delete all queued hook items at once or replay or process them, manage labels, replace or update a single data store record, delete all records of a store, manage keys or organizations (it only reads the bound team
and its organization; in organization mode it lists, creates, updates, and, through a tools list, deletes teams of the bound organization, and nothing else; changing an organization, inviting or removing members, and organization variables are not offered), or read or change organization variables; those are out of scope. It lists, creates, updates, and (through a tools list) deletes the custom variables of the bound team, never a system variable. For connections it
lists and reads allow-listed metadata, lists editable parameter names, tests, renames, and (through a tools
list) creates, sets the data of, and deletes a connection of the bound team, taking secrets only from a
released forward credential, and lists and sets the roles of users on its access list; it does not authorize
an OAuth connection, create a locked connection, accept a secret as an argument, or remove access-list
members, or lock or unlock a connection. For
credential requests it creates, lists, reads, and (through a tools list) deletes them; it does not decline,
reauthorize, or reset a request's credentials, list app modules, or read request details with their credential
states. It invites no new user: only members of the bound team can be the provider. For data stores it lists, reads, creates, updates, and (through a tools list) deletes a store of the bound team and lists and reads data structures; it lists, creates, and (through a tools list) deletes explicitly named records of a store, never deletes several stores at once, and never creates, changes, or deletes a data structure.
