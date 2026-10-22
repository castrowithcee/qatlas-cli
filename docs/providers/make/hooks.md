---
description: >
  Describes the Make hook tools: the hook reads and their team and scenario binding, trigger secrets and the one tool
  that returns them, the hook queue and its payload caps, and renaming, enabling, disabling, and deleting hooks.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Make: hooks

The hook tools (`make.hooks.*`) and queue tools (`make.hook-queue.*`) work on the webhooks and mailhooks of the bound
team. Errors follow [Errors](README.md#errors).

## Hooks

The five read tools (`make.hooks.list`, `get`, `ping`, `logs`, and `url`) need the `hooks:read` scope (a 403 names
it); the `hooks-read` profile holds all of them but `url`. `make.hooks.list` always sends the bound
team as `teamId`. Every tool that names a `hook_id` validates it locally (positive integer), reads the hook
from Make first, and refuses it as an invalid request, without naming its real team, when its `teamId` is not
the bound team, before any ping, log, or URL request is sent. On a connection with a scenario allow-list the
hook tools are narrower than the scenario tools: only hooks assigned to a listed scenario are listed or
reachable, and an unassigned hook is not. A hook names no folder, so a folder target reaches no hook.
`make.hooks.ping` reads the hook's status (attached, learning, gone) and sends no data to the hook.

The trigger URL, a mailhook address, the hook's `udid`, and `ping.address` are trigger secrets: anyone who
knows them can start the scenario. `list`, `get`, `ping`, and `logs` omit them and mask any part of them inside
the strings they return. Only `make.hooks.url` returns the URL; it is not part of any profile and needs a
connection whose `tools` list names it. Its value is deliberately not added to the output redactor, since that
would blank the tool's own answer. Hook logs are reduced to metadata (id, status, time, replayable flag, type,
and at most eight integer sizes); request headers, bodies, parsers, and udids are never read into a result.

### Hook queue

The queue tools `make.hook-queue.list`, `get`, `stats`, and `delete` each read the
hook first and refuse a hook of another team, or outside a scenario allow-list, before any queue request.
The `hookqueue-read` profile (list, get, stats) needs `hooks:read`. Queued payloads are untrusted and may hold
personal data: they carry the data sensitivity `make-webhook-payloads-personal-data`. `list` returns metadata only,
never payloads. `get` returns the payload capped to
6 levels, 200 values, 256-byte strings, and 8 KiB, with `truncated` set when anything was cut or the whole
payload had to be dropped; the hook's trigger URL, `udid`, and mailhook address are masked inside it, and the
values of `authorization`, `cookie`, `set-cookie`, `host`, `referer`, and `origin` keys are replaced. Incoming
ids must be 1 to 64 letters, digits, underscores, or hyphens, checked before any secret or request.

`make.hook-queue.delete` needs `hooks:write` (with `hooks:read`, which binds the hook), is in no profile, and is
offered only when a connection's `tools` list names it. It takes 1 to 50 distinct explicit `ids`, needs its own
confirmation, sends exactly one
`DELETE` with only `ids` (never `all`, `exceptIds`, or Make's `confirmed` flag, which Make requires only for
deleting everything), and is never repeated. After a 5xx, a dropped connection, an unreadable answer, or an
error in the answer it reports that items may have been deleted; the queue should be read before trying again.

### Changing hooks

`make.hooks.rename`, `enable`, `disable`, and `delete` need `hooks:write` (a 403 names it) and
`hooks:read`, which binds the hook. The `hooks-manage` profile holds the hook reads (without `url`) plus rename,
enable, and disable; `make.hooks.delete` is in no profile and is offered only when a connection's `tools` list names
it. Each needs its own confirmation, sends exactly one changing request, and
is never repeated: a 5xx, a dropped connection, or an unreadable answer is reported as uncertain, and the
current state should be read before trying again. Every tool that names a `hook_id` reads the hook first and
refuses it, before the changing request, unless its `teamId` is the bound team (and, with a scenario
allow-list, its scenario is listed). Enable and disable re-read the hook and report an unconfirmed state as
uncertain; a result outside the bound team after the change is a provider error.

`make.hooks.delete` sends `confirmed=true` only when `confirm_scenarios_affected` is true. Without it, Make
refuses the deletion of a hook a scenario uses; Qatlas then answers `deleted: false`,
`confirmation_required: true`, and up to 20 scenarios (ids, and names bounded and masked, untrusted) taken
from the refusal body when it lists any and from the hook read, and does not repeat the request. Make
documents only that an error is returned; its exact shape is not specified, so a refusal is recognized as a
4xx answer when scenarios are known, and any other failure stays an error.
