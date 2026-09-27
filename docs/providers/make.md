---
description: >
  Describes the Make provider: zone and API token setup, the required team target plus the optional
  organization and scenario allow-lists and their live team-membership check, the scenario and blueprint
  reads, the run (Make's own "logs") reads and their offset pagination, the bounded best-effort run error,
  and the scope and plan boundaries of a Make API token.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-09-27
---

# Make

Make is a provider for one Make (make.com) zone's REST API (`/api/v2`). It lists and reads scenarios, reads
a scenario's blueprint, and lists and reads its run history.

**There is no tool yet to create or change a scenario, activate or deactivate it, or run it on demand.**
Those, and scenario deletion, cloning, and every other write, are a later milestone.

## Configuration

`base_url` is the Make zone this connection reaches: `https://eu1.make.com`, `https://eu2.make.com`,
`https://us1.make.com`, `https://us2.make.com`, `https://eu1.make.celonis.com`, or
`https://us1.make.celonis.com`, the zones Make documents as of this writing. It must be exactly one of
these hosts, plain `https`, without a port, path, user, query, or fragment; Qatlas refuses any other URL
before a secret is read, and never falls back to a free-form origin the way a self-hosted provider's base
URL can be.

The credential provides `api-token`, a Make API token created under the profile avatar, Profile, API, Add
token, with at least the `scenarios:read` scope. A Make token belongs to exactly one zone: Make's own
guidance is to create a separate token for each zone a person has access to, so a token created for a
different zone than this connection's own is rejected here the same way as any other invalid token. Within
its zone, a token reaches every team its owner belongs to, which is why this connection's own team target,
not the token, decides what is exposed.

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
| `team/TEAM_ID` | the one Make team this connection may reach; required, exactly one |
| `organization/ORG_ID` | the organization the bound team belongs to; optional, at most one |
| `scenario/SCENARIO_ID` | one scenario of the bound team; optional, repeatable |

```yaml
connections:
  customer-a-team-x:
    service: make-customer-a
    credential: make-customer-a-reader
    targets: [team/123456, organization/7890]
```

Team is **required**, unlike the optional allow-lists the n8n and Infomaniak providers offer: a Make API
token carries no narrower scope of its own than "every team its owner belongs to", so the connection's own
team target is the only boundary that exists at all, not an extra layer on top of one Make already gives it.

A `scenario_id` argument outside a configured scenario allow-list is refused locally, as an invalid request,
before any request is sent. A scenario's team membership is checked live, against Make's own answer, never
against local configuration alone: `make.scenarios.get`, `make.scenarios.blueprint`, `make.runs.list`, and
`make.runs.get` all read the named scenario from Make first and confirm its reported `teamId` matches the
bound team before any of that scenario's content, blueprint, or run history is returned. A scenario of
another team is refused the same way as one outside the allow-list, and the refusal never names the
scenario's real team.

A scenario object itself reports no `organizationId`, only `teamId`, so a configured organization target
cannot be checked against a scenario directly. `make.runs.list` and `make.runs.get` can check it: a run's
own answer carries both `teamId` and `organizationId`, and both are defensively re-applied to every run this
provider returns, even though the up-front scenario check already covers the team on its own.

## Tools

| Tool | Effect | Does |
| --- | --- | --- |
| `make.scenarios.list` | read | lists the scenarios of the bound team, filtered to the scenario allow-list, page by page |
| `make.scenarios.get` | read | reads one scenario, with its team membership confirmed live |
| `make.scenarios.blueprint` | read | reads one scenario's blueprint (modules, wiring, configuration) |
| `make.runs.list` | read | lists one scenario's run history, page by page |
| `make.runs.get` | read | reads one run's status, timings, operations, data volume, and a bounded best-effort error |

Every tool of this milestone is `read`, safe, and needs no confirmation.

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

## Runs and their errors

`make.runs.get` reads one run's status, timings, operations, and data volume from Make's own log entry, and,
only when that entry's `detail` object happens to carry the recognised `detail.error.message` shape, a short
excerpt of it, bounded to 2048 characters. Make does not document the `detail` object's shape field by
field, so this extraction is deliberately conservative: anything else `detail` may carry, including the
versioned scenario-history change Make attaches to a "modify"-type log entry, is never surfaced, never
passed through raw, and never guessed at. A run never returns a bundle's actual input or output data, which
this endpoint does not expose in the first place.

## Errors

Errors keep stable classes and never carry the API token or a raw provider response body:

| Class | Cause |
| --- | --- |
| `auth` | Make rejected the API token, including a token created for a different zone than this connection's own |
| `permission` | this API token may not perform the operation; it needs the `scenarios:read` scope |
| `not-found` | Make does not hold the resource or does not show it to this token |
| `rate-limited` | Make rate-limited the request; Qatlas applies no proactive spacing of its own and instead holds its own limiter for whatever `Retry-After` Make names |
| `timeout` | Make did not answer in time |
| `unreachable` | Make is unavailable, in maintenance, or could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, or named a different resource than the one requested |
| `provider-error` | every other rejection, including a redirect on an endpoint that must not answer with one |

A `scenario_id` outside the connection's allow-list, and a scenario, or a run, the live team or organization
check finds outside the bound scope, are invalid requests, never provider errors, so a scope refusal is
never mistaken for a missing scenario or run.

## Untrusted data

Scenario names, descriptions, scheduling configuration, blueprint content, and every other value a listing
or a read answers with come from the zone and are untrusted data. Qatlas normalises them into a stable
envelope and never renders them, follows a link inside them, or executes anything derived from them.

## Boundary

This provider lists and reads scenarios, reads a scenario's blueprint, and lists and reads its run history.
It does not, and has no tool to, create or update a scenario, activate or deactivate one, run one on demand,
delete or clone a scenario, manage folders, labels, data stores, hooks, keys, connections, teams, or
organizations, or read team or organization variables; those are deliberately out of this milestone.
