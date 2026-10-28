---
description: >
  Describes the GitHub repository tools: the repository list of an owner, repository create, fork, and delete, collaborators, rulesets, custom properties, repository merge and branch settings, starred repositories, and the repository search.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-28
---

# GitHub repositories

The repository tools of the [GitHub provider](README.md). Every tool takes `repository`, `owner`, or
`organization` as described under [the target of a call](README.md#the-target-of-a-call); a change needs the
confirmation described under [confirmed changes](README.md#confirmed-changes).

## Owners: repositories

`github.repositories.list` lists the repositories the owner owns (`users/LOGIN` or `orgs/LOGIN`), in name
order unless `order_by` says otherwise, archived ones included. A user's repositories are the ones the user
owns, not those the user collaborates on: the tool's purpose fixes that binding, so there is no
`affiliations` or `ownerAffiliations` argument. What the list shows depends on the targets, see
[owner lists](README.md#owner-lists). The token rights are listed under [Repositories, rulesets, and properties](setup.md#repositories-rulesets-and-properties). `privacy` and `visibility`
cannot be combined, as GitHub refuses the pair; every filter binds the cursor.

Each entry carries the members of GitHub's GraphQL repository in the one request a call sends. A list or a
connection is read with its first page, at most 10 entries per repository, together with its total count and
a truncated marker; a page of 100 repositories then asks for 6100 nodes, far below GitHub's limit of 500000.
Connections that have a list of their own (issues, pull requests, releases, collaborators, and the like)
report only their count: a connection GitHub also serves as a list of its own is read with that list. The
`viewer_*` members describe the authenticated user, not the repository.

- **Parent and template:** a repository outside the connection's targets appears only with its identifiers.
- **Owner:** the entry does not repeat the owner, since the object a call names is fixed by the call.
- **Deploy keys:** only their number is reported, since deploy keys are public keys that grant access.
- **Clone token:** `tempCloneToken` is never read, since it grants clone access without a sign-in.
- **Privileged members:** members that can need push or admin rights (collaborators, deploy keys, branch
  protection rules, vulnerability alerts, and the like) are read only with `admin_counts`, since GitHub refuses
  them with an error and a refused member of a non-null type empties the whole repository, which would fail a
  list that works without them. With `admin_counts`, a member refused inside one repository is left out of that
  entry and named in `unreadable`; a refusal of the list itself still fails it.
- **Left out:** deprecated members, rendered variants that repeat a text in another format, and members that
  need a lookup argument.

## Repository lifecycle and collaborators

| Tool | Does |
| --- | --- |
| `github.repositories.create` | creates one repository under the token's own account or an organization |
| `github.repositories.fork` | forks a repository into the token's own account or an organization |
| `github.repositories.delete` | deletes one repository permanently; offered only where a connection's `tools` list names it |
| `github.collaborators.list` | lists the collaborators of a repository |

`github.repositories.create` makes one new repository under the token's own account, or, with `owner`, under
an organization a connection names as an owner target; it is private by default. `owner` is an organization as
`orgs/LOGIN`; a user login is refused, since GitHub creates a repository only under the token's own account or
an organization. Left out, `owner` defaults to the connection's one organization owner target, when it names
exactly one; otherwise the repository is created under the token's own account. The answer's `owner` names the
account it actually landed under. A name already taken under the destination account answers
`invalid-request` with a clear message instead of a second attempt, since GitHub refuses it with HTTP 422.

`github.repositories.fork` forks a repository a connection allows into the token's own account or, with
`organization` (as `orgs/LOGIN`, the same restriction and target check as `owner` above), into an organization
the connection names as an owner target. `organization` has no default, unlike create, since a fork always
names a specific source repository and never falls back to a target it merely happens to allow. GitHub answers
asynchronously, so a large repository may still be importing once the call returns. Forking a repository
already forked into the same account does not create a second fork, so the tool is idempotent.

Creating or forking under the token's own account, rather than an organization, reads the account behind the
token as a whole and follows the [account-wide rule](accounts.md#account-wide-tools): it is refused when the
connection's targets name a repository or a project, since the account behind the token may belong to a
customer other than the one those targets name.

`github.repositories.delete` deletes one repository permanently and is offered only where a connection's
`tools` list names it. `confirm_name` must repeat the repository as `owner/name` exactly, checked before any
credential is resolved, since this cannot be undone.

`github.collaborators.list` reads the collaborators of a repository, paged, a cursor bound to the repository
and its filters.

The token rights are listed under [Repositories, rulesets, and properties](setup.md#repositories-rulesets-and-properties).

The terminal editor's setup profile `repository-admin-reader` (not recommended) selects
`github.collaborators.list`, `github.rulesets.list`, `github.rulesets.get`, and `github.custom-properties.get`;
it is not the recommended profile, which stays `read`, unchanged.

## Rulesets: repository and organization governance

| Tool | Does |
| --- | --- |
| `github.rulesets.list` | lists the rulesets of a repository or an organization |
| `github.rulesets.get` | reads one ruleset |
| `github.rulesets.create` | creates one new ruleset; offered only where a connection's `tools` list names it |
| `github.rulesets.update` | replaces one ruleset as a whole; offered only where a connection's `tools` list names it |
| `github.rulesets.delete` | deletes one ruleset permanently; offered only where a connection's `tools` list names it |

The reads cover the rulesets of a repository or, with `organization` instead of `repository`, of an
organization a connection allows. The three changes are listed only, since a ruleset governs what a repository
or an organization allows at all.

Every call takes exactly one of `repository` (as `OWNER/REPO`, checked against the connection's repository
targets like every other repository tool) or `organization` (as `orgs/LOGIN`, checked against its owner
targets like [`github.teams.list`](accounts.md#organization-teams)); giving both or neither is an invalid
request, checked before a credential is resolved, so a call never falls back to a target it did not name. A
ruleset identifier from one target is unknown under another: `github.rulesets.get`, `.update`, and `.delete`
answer `not-found` for an `id` GitHub does not hold under the given repository or organization, whichever
GitHub does not distinguish from one it merely does not show to this token.

`includes_parents` applies to a repository only: the list ignores it for an organization, which has none to
inherit from, while the single read refuses it there, since dropping it silently would hide a mistaken call.
A list cursor is bound to the target and to every filter, so a cursor of other filters is refused.

In a list entry, `source` stays the repository or organization the list was read from and `source_name` is
where GitHub configures the ruleset, which differs for a ruleset inherited from an organization. GitHub's list
may omit the conditions, rules, and bypass actors; the list reports them where GitHub sends them and leaves
one out, marked, when it exceeds a bound, because a cut rule would state another rule.

`github.rulesets.create` and `github.rulesets.update` take a ruleset definition. `target` `repository`
selects repositories instead of refs for an organization ruleset. Every field is GitHub's own shape, passed
through as given and read back the same way, untrusted data; only the top-level required fields, the enums,
and the array and byte-size bounds are checked before a credential is resolved. `github.rulesets.update`
sends the whole definition in one `PUT` that replaces the ruleset as a whole and is idempotent as long as
nothing else changes the ruleset between two identical calls; `github.rulesets.create` is not, since a
repeated call creates a second ruleset.

## Custom properties

| Tool | Does |
| --- | --- |
| `github.custom-properties.get` | reads the property values of a repository or the property schema of an organization |
| `github.custom-properties.set` | sets or removes property values, or creates or replaces property definitions; offered only where a connection's `tools` list names it |

Every call takes exactly one of `repository` or `organization`, the same exclusive selection, checks, and
refusals as [Rulesets](#rulesets-repository-and-organization-governance).

`github.custom-properties.get` reads the custom property values of a repository or, with `organization`, the
custom property definitions (schema) an organization declares. It never reads the values GitHub holds for the
repositories of an organization: that GitHub route names repositories by a bare list of names the caller
supplies, and binding that list to the connection's own repository targets would need a second, independent
allow-list check this tool does not perform, so the narrower reading is a schema read only.

`github.custom-properties.set` is listed only, since a custom property can gate what a ruleset condition
matches. For a repository it takes `properties` and sets or removes property values, a `value` of `null`
removing one; for an organization it takes `definitions` and creates or replaces property definitions, the
smaller scope that already covers the official custom properties write for a single repository, since that is
offered through the repository target; only one of `properties` or `definitions` may be given, matching the
exclusive target. A name the organization already declares is replaced, so a repeated call is idempotent.
GitHub answers a repository set with no body, so the tool echoes back the values sent; an organization set
answers the definitions GitHub created or replaced. Every field is checked before a credential is resolved:
the exclusive target and argument, the array bounds a JSON schema's `maxItems` cannot enforce by itself, and
the size of the whole request.

## Repository merge and branch settings

| Tool | Does |
| --- | --- |
| `github.repository-settings.get` | reads the merge and branch settings of one repository |
| `github.repository-settings.update` | changes the merge and branch settings of one repository |

Both belong to the Actions administrator group, so they are listed only, never part of a starting profile, and
chosen with the profile `actions-admin` or by name in a `tools` list. Both act on one repository inside the
connection's targets; a repository outside them is refused before a secret is read or GitHub is contacted,
without naming it.

`get` answers exactly the merge and branch fields of its output schema and nothing else of the repository.
GitHub leaves the merge fields out for a token without write access; they are absent then.

`update` accepts only the merge and branch arguments of its input schema. At least one value is required, and
only the values given are sent in one `PATCH` request that is never retried; after a timeout, a server error,
or an unreadable answer the refusal says the change may have been applied. Name, description, visibility,
default branch, archiving, feature switches, security settings, transfer, deletion, rulesets, and the Actions
settings are not reachable. Giving `allow_merge_commit`, `allow_squash_merge`, and `allow_rebase_merge` all as
`false` is refused locally, before any request; the current state is not read for that check. The answer names
the settings the call gave with the values GitHub reports, and carries no `before` value, since the change is
the only request. A refusal for a missing right names the permission below, never text from GitHub.

The token rights are listed under [GitHub Actions](setup.md#github-actions), whose rule for a token of its own they follow.

## Repository subscriptions

| Tool | Does |
| --- | --- |
| `github.repository-subscriptions.set` | watches, ignores, or unsubscribes a repository |

The tool belongs to the [notifications](notifications.md#notifications) of the account behind the token and
follows their rules for confirmation, idempotency, and the setup profile `notifications`. It takes
`repository` like the other repository tools. The repository subscription is GitHub's watching endpoint; see
[Repositories, rulesets, and properties](setup.md#repositories-rulesets-and-properties) for its token.

## Stars

| Tool | Does |
| --- | --- |
| `github.stars.list` | lists the repositories the account has starred, in batches, with the time each was starred and the repository in full |
| `github.stars.add` | stars one repository; a repository already starred is left as it is |
| `github.stars.remove` | unstars one repository; a repository that is not starred is left as it is |

`github.stars.list` reads data of the account behind the token as a whole and follows the
[account-wide rule](accounts.md#account-wide-tools); on a connection without repository or project targets, it
narrows its answer to the starred repositories of the owners the targets name, when it names any. It asks
GitHub for the star media type, which is what carries the star time; `sort` and `direction` reach GitHub only
when set, so the order stays GitHub's own by star time otherwise, and bind the cursor only while set. The
repository follows the [result object rules](README.md#result-objects).
`github.stars.add` and `github.stars.remove` take `repository` like every other repository tool and check it
against the targets before a secret is read.

Starring and unstarring are naturally idempotent: GitHub answers both with no content whatever the
repository's previous state. Before sending either, Qatlas reads whether the repository is already starred;
a repository already in the requested state is reported with `changed: false` and nothing further is sent.
Otherwise the change travels once, as every change here does, and the answer names the state afterward.

The token rights are listed under [Stars](setup.md#stars).

The terminal editor's setup profile `stars` selects `github.stars.list`, `github.stars.add`, and
`github.stars.remove`; every star or unstar needs its own confirmation. It is not the recommended profile,
which stays `read`, unchanged. The profile `discovery` is described under [accounts](accounts.md#account-wide-tools).

## Search

| Tool | Searches |
| --- | --- |
| `github.repositories.search` | repositories, optionally sorted by `sort` and `order`, with `text_matches` on request |

It reads GitHub's own search index with `terms`, under the rules described in [Search](search.md), and
forces the [target qualifiers](search.md#target-scoping). `sort` and `order` are arguments of their own, never
part of `terms`, and GitHub's best match order stays when they are omitted. A match carries the repository
under the [result object rules](README.md#result-objects), beside its compact members. The code and commit
search are described with the [code](code.md#search), the user and organization search with the
[accounts](accounts.md#search).
