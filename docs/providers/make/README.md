---
description: >
  Describes the Make provider: zone and API token setup, the required team target plus the optional organization,
  folder, and scenario allow-lists, team and organization mode and the live scenario-team check, how targets are
  chosen, the error classes, the untrusted data rule, and the boundary of the provider. Links to the pages for each
  area.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-27
---

# Make

Make is a provider for one Make (make.com) zone's REST API (`/api/v2`). It lists and reads scenarios, reads
a scenario's blueprint, lists and reads its run history, and, with confirmation, creates a scenario, replaces
a scenario's blueprint, scheduling, name, or folder, starts and stops it, and runs it on demand. Separate
read-only profiles read the bound team and its organization.

**There is deliberately no tool to delete, clone, or replay a scenario, and no generic webhook call.** A
Make scenario, once created, cannot be removed again by this provider; deleting one still requires the Make
UI or a direct API call outside Qatlas.

Qatlas creates no permanent access or data paths to the outside (webhooks, invitations, public links); reading,
pausing, revoking, and deleting remain. Hence no tool creates a hook or invites a person.

The token scopes, how to create the token, and fixing errors are on [Set up Make](setup.md).

Pages by area:

- [Scenarios and runs](scenarios.md): pagination, blueprints, starting and stopping, folders, runs and their errors
- [Team and organization](organization.md): the bound team, teams of an organization, organization master data,
  financial data, team and organization variables
- [Hooks](hooks.md): hooks, the hook queue, changing hooks
- [Connections](connections.md): connections, creating them and setting their data, access lists, credential requests
- [Data stores](data-stores.md): data stores, data structures, and data store records

Every read is safe and needs no confirmation. Every change of the manage profile needs its own confirmation,
sends exactly one changing request, and is never retried by this provider itself: a failure that could mean
the request nonetheless reached Make (a timeout, a connection reset, or a 5xx) is reported as uncertain
instead, naming what to check before trying again.

## Configuration

`base_url` is the Make zone this connection reaches: `https://eu1.make.com`, `https://eu2.make.com`,
`https://us1.make.com`, `https://us2.make.com`, `https://eu1.make.celonis.com`, or
`https://us1.make.celonis.com`, the zones Make documents as of this writing. It must be exactly one of
these hosts, plain `https`, without a port, path, user, query, or fragment; Qatlas refuses any other URL
before a secret is read, and never falls back to a free-form origin the way a self-hosted provider's base
URL can be.

The credential provides `api-token`, a Make API token; creating it and the scopes each tool group needs are on [Set
up Make](setup.md). A Make token belongs to exactly one zone: Make's own guidance is to create a separate token for
each zone a person has access to, so a token created for a different zone than this connection's own is rejected
here the same way as any other invalid token. The token carries API scopes, while Make also limits resources by the
user's team membership. This connection's required team target adds a Qatlas boundary within that access. Make also
documents optional locked connections with their own access lists; this feature must be enabled for the
organization; this provider enforces none of them itself and only reads and sets their members through the
access-list tools. See Make's [Connections API
reference](https://developers.make.com/api-documentation/api-reference/connections.md) and [connection access-list
reference](https://developers.make.com/api-documentation/api-reference/connections/access-list.md).

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
| `folder/FOLDER_ID` | the scenarios in one folder of the bound team and in all folders below it; optional, repeatable; team mode only |
| `scenario/SCENARIO_ID` | one scenario of the bound team; optional, repeatable; team mode only |

A connection has exactly one of two modes. **Team mode** (the default described here): exactly one `team/TEAM_ID`,
an optional `organization/ORG_ID`, and optional folders and scenarios. **Organization mode**: no team and exactly one
`organization/ORG_ID`; scenario and folder targets are refused (the narrower reading, since both belong to a team).
Configuration errors never quote a configured value. In organization mode every team tool (all tools except
`make.teams.*`, including `make.team.*` and `make.organization.get`) is refused as an invalid request ("needs a
team connection") before a secret is read or a request is sent; in team mode the `make.teams.*` tools are
refused the same way ("needs an organization connection").

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

Folder and scenario targets form one allow-list, their union; without either, the whole team is reachable.
A `scenario_id` argument outside a configured scenario allow-list is refused locally, as an invalid request,
before any request is sent; with a folder target the decision falls to the scenario read below, which proves
the folder from the `folderId` Make reports for the scenario. A listed folder needs no further request; for
any other folder, one read of the team's folder tree per call (`GET /scenarios-folders`) proves whether it lies
below a listed folder, and `make.scenarios.list` filters its rows the same way. A scenario outside every listed
folder and its subfolders, or in none, is refused without naming its folder. `make.scenarios.update` moves a
scenario that is in scope only through its folders solely into a listed folder or one below it, and never
clears its folder. A scenario's team membership is checked live, against Make's own answer, never
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
or folder allow-list, since a scenario that does not exist yet can never already be on that list.
`make.scenarios.create`, `make.scenarios.update`, `make.scenarios.start`, and `make.scenarios.stop` all
re-read the scenario after their one changing request and re-apply the bound team and scenario allow-list to
it; a mismatch is reported as a `provider-error`, not an invalid request, because the change has already
happened and this provider has no delete tool to undo it. `make.scenarios.run` is never followed by a
re-read: its own answer carries no scope of its own to re-verify (see
[Runs and their errors](scenarios.md#runs-and-their-errors)).

### Exclusions

`exclude` takes objects out of what the targets reach. Its entries are spelled like targets but stand apart from
them: `scenario/SCENARIO_ID` and `folder/FOLDER_ID` in team mode, `team/TEAM_ID` in organization mode. Any other
entry (an organization, a team in team mode, a scenario or folder in organization mode), a repeated entry, and an
entry equal to a target are invalid. Configuration validation does not know the mode, so the provider checks the
list on every call, before a secret is read, and never quotes a value. An exclusion is no target: it restricts no
tool, changes which tools a binding reaches in no way, and wins over every target.

Make knows no rights below the team, so an excluded scenario, and every scenario in an excluded folder or any folder
below it, is treated like a scenario of another team: a tool that names it refuses it as outside the targets without
naming the scenario or the exclusion, `make.scenarios.list` leaves it out (also with `ids`), and a `folder_id`
filter on an excluded folder gives an empty page. `make.scenarios.create` with such a `folder_id` and
`make.scenarios.update` moving a scenario into such a folder are refused before any request; a result that lands
there anyway is a `provider-error`, as for targets. Moving a reachable scenario elsewhere stays allowed. The folder
tree is read once per call for every path that must place a scenario or hook, only on a connection that excludes a
folder; a folder the tree does not show counts as excluded. Hooks of excluded scenarios are covered under
[Hooks](hooks.md#hooks), excluded teams under [Teams of an organization](organization.md#teams-of-an-organization).

Tools of objects without a scenario reference (connections, access lists, credential requests, data stores, their
records, data structures, team variables) are unchanged, except that a refused connection or data store deletion
lists only reachable scenarios. A blueprint is the scenario's own content: a reachable scenario's blueprint may
still name an excluded sub-scenario, and Qatlas does not remove it.

## Choosing targets

A person chooses the targets of a connection level by level: organization, the teams of an organization, then
the folders of a team and its scenarios. The scenario list of a team holds all its scenarios, in a folder or
not, so a scenario outside any folder stays selectable; its folder is shown as its place. Organizations and
teams page by offset (`pg[offset]`, `pg[limit]`); the folder list of a team comes in one answer with all
subfolders, since Make documents no paging for it. Scenarios can be searched by name at Make; the other levels are
filtered by what has been loaded. The connection's own targets and exclusions neither filter nor narrow what is
offered, and an exclusion is never offered as a target.
Choosing only reads, through no tool and for no agent. Names are untrusted data and are shortened.

No URL of Make's web interface is resolved: neither Make's API documentation nor its help pages document the
routes for team, scenario or folder, so a pasted URL names no target.

Which tools a binding reaches follows what the call refuses. A team binding reaches no organization tool
(`make.teams.*`, `make.organization-variables.*`, `make.organization.update`, `.members`, `.subscription`,
`.usage`, `.payments`); an organization binding reaches nothing else. A scenario or folder allow-list reaches
no `make.scenarios.create`, connection, credential request, data store, data structure or team variable tool;
a folder target also reaches no hook or hook queue tool.

## Errors

Errors keep stable classes and never carry the API token. Qatlas' message stays free of provider text; Make's own
answer (status, `code`, `message`, and each `suberrors` message and name) goes along separately as the original
statement in the error detail's `provider` field, marked untrusted, redacted, and bounded:

| Class | Cause |
| --- | --- |
| `auth` | Make rejected the API token, including a token created for a different zone than this connection's own |
| `permission` | this API token may not perform the operation; the message names the scope(s) needed, listed in [Scopes per tool group](setup.md#scopes-per-tool-group); [Fixing errors](setup.md#fixing-errors) gives the remedy |
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

## Untrusted data

Scenario names, descriptions, scheduling configuration, blueprint content, run status, and every other value
a listing, a read, or a change answers with come from the zone and are untrusted data, whether this provider
read it or a caller supplied it for a create, update, or run. Qatlas normalises it into a stable envelope and
never renders it, follows a link inside it, or executes anything derived from it.

## Boundary

This provider lists and reads scenarios and runs, reads a scenario's blueprint, creates a scenario, replaces
a scenario's blueprint, scheduling, name, or folder, starts and stops a scenario, and runs one on demand. It
deliberately does not, and has no tool to, delete or clone a scenario, replay a run, make a generic webhook
call, start or stop a hook's learning mode, set a hook's data, delete all queued hook items at once or replay or
process them, manage labels, delete all records of a store, manage keys, create or delete an organization, or invite
or remove members; those are out of scope. It lists, reads, pings, renames, enables, and disables hooks of the bound
team and (through a tools list) deletes a hook or named queue items.

In team mode it reads the bound team and its organization; in organization mode it lists, creates, updates, and
(through a tools list) deletes teams of the bound organization and changes the organization's name, country, and
time zone. It lists, creates, updates, and (through a tools list) deletes the custom variables of the bound team or,
in organization mode, of the bound organization, never a system variable. For connections it lists and reads
allow-listed metadata, lists editable parameter names, tests, renames, and (through a tools list) creates, sets the
data of, and deletes a connection of the bound team, taking secrets only from a released forward credential, and
lists and sets the roles of users on its access list; it does not authorize an OAuth connection, create a locked
connection, accept a secret as an argument, or remove access-list members, or lock or unlock a connection. For
credential requests it creates, lists, reads, and (through a tools list) deletes them; it does not decline,
reauthorize, or reset a request's credentials, list app modules, or read request details with their credential
states. It invites no new user: only members of the bound team can be the provider. For data stores it lists, reads,
creates, updates, and (through a tools list) deletes a store of the bound team and lists and reads data structures;
it lists, creates, replaces, updates, and (through a tools list) deletes explicitly named records of a store, never
deletes several stores at once, and never creates, changes, or deletes a data structure.
