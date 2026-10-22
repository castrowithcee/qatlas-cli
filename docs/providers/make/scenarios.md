---
description: >
  Describes the Make scenario and run tools: offset pagination and its heuristic has_more, blueprint and scheduling
  encoding and references, starting, stopping, and folder moves, and the run tools with the bounded best-effort run
  error and the uncertain-result rules of an on-demand run.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Make: scenarios and runs

The scenario tools (`make.scenarios.*`) and run tools (`make.runs.*`) work on the scenarios of the bound team. The
target rules, the live team check, and the allow-list refusals are in [Scope](README.md#scope); the error classes are in
[Errors](README.md#errors). Scenario, blueprint, and run values are untrusted data
([Untrusted data](README.md#untrusted-data)). `make.scenarios.update` is partial: only the fields given are replaced.

## Pagination

Both list tools take `offset` and `limit` (1 to 200; 50 when omitted) and answer `offset`, `count`, and
`has_more`. Make's own pagination object (`pg`) echoes the request's offset and limit but reports no total
count, so `has_more` is a heuristic, not a value Make states: it is true exactly when a page came back full,
the same convention the SeaTable provider's own offset pagination uses. `make.scenarios.list` also forwards
`is_active` and `name` to Make's own filters of the same kind. `make.runs.list` also accepts
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
nested object, and is sent as one, unencoded (see [Runs and their errors](#runs-and-their-errors)).

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
also runs it if it is scheduled at regular intervals ([scenario API
reference](https://developers.make.com/api-documentation/api-reference/scenarios.md)). The reference does not say
whether calling `/start` on an already active, regularly scheduled scenario triggers another run. Treat that effect
as unverified and do not infer that repeating start is safe to retry. `make.scenarios.run` independently requests an
on-demand run.

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
the ordinary status classification in [Errors](README.md#errors) rather than a fabricated, scenario-specific one.
