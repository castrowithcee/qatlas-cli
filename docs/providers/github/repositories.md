---
description: >
  Describes the GitHub repository tools: the repository list of an owner, the details, languages, contributors, and community profile of one repository, its statistics and activity feed, repository create, fork, rename, and delete, collaborators, repository traffic, rulesets, custom properties, repository settings, starred repositories, and the repository search.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-11-07
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

## Repository details, languages, contributors, and community profile

| Tool | Does |
| --- | --- |
| `github.repositories.get` | reads one repository with every detail GitHub reports |
| `github.languages.get` | reads the languages of a repository with their bytes of code |
| `github.contributors.list` | lists the contributors of a repository with their number of commits |
| `github.community-profile.get` | reads the community profile of a repository |

All four read one repository of the connection through a fixed GitHub route, take `repository` like every other
repository tool, and check it against the targets and exclusions before a secret is read or GitHub is contacted,
without naming it. They write nothing, and names, descriptions, and addresses GitHub reports are untrusted data
that is never fetched. The token rights are listed under
[Repositories, rulesets, and properties](setup.md#repositories-rulesets-and-properties). The setup profiles
`repository-reader` and `repository-writer` select them; the recommended profile stays `read`.

`github.repositories.get` reads GitHub's full repository over REST rather than the GraphQL projection of the
owner list, since REST carries the whole documented object in one request, including the settings, merge
options, `network_count`, `subscribers_count`, custom properties, and the security and analysis status that the
list reads in part. The answer has the members of the [repository object](README.md#result-objects) of the
other tools plus `organization` as an account. `parent`, `source`, and `template_repository` are repositories
the details link to; one outside the connection's targets, or covered by an exclusion, keeps only `id`,
`node_id`, and `full_name`. `description` is cut, `homepage` and every address are never fetched, and `links`
holds the REST and clone addresses by their GitHub names. Left out are `temp_clone_token`, which grants clone
access without a sign-in, and the deprecated `has_downloads` and `use_squash_pr_title_as_default`. GitHub leaves
the merge settings and `security_and_analysis` out for a token without the rights for them; they are absent then.

`github.languages.get` answers `languages`, each with `name` and `bytes`, largest first and by name among equals,
at most 100 with `languages_truncated`. GitHub answers 202 while it still computes the languages: the tool reports
`state` as `computing` with no languages and neither waits nor repeats the request, so call it again later;
otherwise `state` is `ready`.

`github.contributors.list` lists contributors, most commits first as GitHub orders them, with the members of an
account and `contributions`. `anon` adds anonymous contributors, who have no account and carry only `name` and
`email` with `type` `Anonymous`; both are untrusted data. GitHub lists at most 500 contributors and leaves out
the ones beyond. The list pages by GitHub's Link header with a cursor bound to the repository and `anon`, so a
cursor of other filters is refused. `state` is `ready`, `computing` when GitHub answered 202 while it computes the
list, or `empty` when GitHub answered an empty repository with no content; neither state is retried or waited
for, and neither carries contributors or a cursor.

`github.community-profile.get` answers `health_percentage`, `description`, `documentation`, `updated_at`, and
`content_reports_enabled` (which GitHub sends only for a repository of an organization), and `files` with the
community files by role: `code_of_conduct` (`key`, `name`, `url`, `links`), `code_of_conduct_file`, `license`
(`key`, `name`, `spdx_id`, `node_id`, `url`, `links`), `contributing`, `readme`, `issue_template`, and
`pull_request_template`. A file has `url`, the web address, and `links.url`, the REST address; a role without a
file is absent.

## Repository statistics and activity

| Tool | Does |
| --- | --- |
| `github.stats-contributors.get` | reads the commits, additions, and deletions per week of each contributor |
| `github.stats-commit-activity.get` | reads the commits per week and day of the last year |
| `github.stats-code-frequency.get` | reads the lines added and deleted per week |
| `github.stats-participation.get` | reads the commits per week of everyone and of the owner |
| `github.stats-punch-card.get` | reads the commits per day of the week and hour of the day |
| `github.activity.list` | lists the activity feed: pushes, force pushes, branch creations and deletions, and merges |

All six read one repository of the connection through a fixed GitHub route, are checked against the targets and
exclusions before a secret is read or GitHub is contacted, and write nothing. The token rights are listed under
[Repositories, rulesets, and properties](setup.md#repositories-rulesets-and-properties). The setup profiles
`repository-reader` and `repository-writer` select them.

The five statistics tools answer `state`: `ready`, `computing` when GitHub answered 202 while it computes the
statistic, or `empty` when GitHub answered a repository without commits with no content. Neither state is waited
for or retried, and neither carries data, so call the tool again later. GitHub's raw arrays and tuples are given
named members, and every list is bounded with the cut visible:

- `github.stats-contributors.get` answers `contributors`, most commits first, at most 100 with
  `contributors_truncated`. Each has `author` (an account, absent when GitHub reports none), `total` (commits
  over the whole history), and `weeks`, the most recent 52 oldest first, each with `week` (the Unix timestamp of
  the week start), `additions`, `deletions`, and `commits`, with `weeks_truncated`. For a repository of 10,000 or
  more commits GitHub reports additions and deletions as 0. `author` shows the members GitHub embeds, is never
  looked up further, and its `login` and `name` are untrusted data.
- `github.stats-commit-activity.get` answers `weeks`, at most 52 with `weeks_truncated`, each with `week`,
  `total`, and `days`, the commits of the seven days, Sunday first.
- `github.stats-code-frequency.get` answers `weeks`, the most recent 260 oldest first with `weeks_truncated`, each
  with `week`, `additions`, and `deletions` (a negative number, as GitHub sends it). GitHub refuses a repository
  of 10,000 or more commits with 422, which the tool reports as a failed request.
- `github.stats-participation.get` answers `all` and `owner`, the commits per week, oldest first, each at most 52
  with `all_truncated` and `owner_truncated`.
- `github.stats-punch-card.get` answers `entries`, at most 168 with `entries_truncated`, each with `day` (0 for
  Sunday through 6 for Saturday), `hour` (0 through 23), and `commits`.

`github.activity.list` lists one batch of the feed, newest first unless `direction` is `asc`. The filters are
`ref` (a branch name or a full ref such as `refs/heads/main`), `actor` (a login), `time_period` (`day`, `week`,
`month`, `quarter`, or `year`), `activity_type` (`push`, `force_push`, `branch_creation`, `branch_deletion`,
`pr_merge`, or `merge_queue_merge`), and `direction` (`asc` or `desc`); an unknown value is refused before GitHub
is contacted. Each activity has `id`, `node_id`, `before` and `after` (the commit SHAs of the ref around the
change), `ref` (untrusted data), `timestamp`, `activity_type`, and `actor` (an account, absent when GitHub
reports none, shown as GitHub embeds it and never looked up further). GitHub pages the feed by cursor: `limit`
sets the batch, `has_more` and `next_cursor` continue it, and a cursor of other filters or another repository is
refused; a following page GitHub announces with a cursor Qatlas cannot carry answers `invalid-response` rather
than a feed that looks complete. GitHub does not document which fine-grained permission the feed route checks.

## Repository lifecycle and collaborators

| Tool | Does |
| --- | --- |
| `github.repositories.create` | creates one repository under the token's own account or an organization |
| `github.repositories.fork` | forks a repository into the token's own account or an organization |
| `github.repositories.rename` | renames one repository under its owner; offered only where a connection's `tools` list names it |
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

`github.repositories.rename` gives one repository a new `name` (without the owner) under the same owner and is
offered only where a connection's `tools` list names it; no profile selects it. Both the current repository and
`owner/name` with the new name must lie inside the connection's targets and outside its exclusions, checked
before any credential is resolved; a refusal names neither. The new name follows the rules of a repository
name, a change of letter case alone is allowed, and the same name is refused. The tool sends one request, never
repeated; after a timeout, a server error, or an answer that does not name the requested owner and name, it
reports that the rename may have been applied, so read the repository before trying again. A name already taken
under the owner answers `invalid-request`. `repository` in the result is the new name, `previous` the old one.

GitHub forwards the old name on the web and for Git, but Qatlas never follows it: after a rename, the old name
no longer addresses the repository in any tool. Targets and exclusions that name the repository without `*`
stay as written and do not match the new name, so a user adjusts them. With such targets, add the new name
before the rename, as the tool refuses a new name outside the targets.

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

## Repository settings

| Tool | Does |
| --- | --- |
| `github.repository-settings.get` | reads the description, homepage, topics, feature switches, default branch, visibility, archived state, and merge settings of one repository |
| `github.repository-settings.update` | changes the description, homepage, feature switches, template and forking flags, default branch, archived state, and merge settings of one repository |
| `github.repository-topics.set` | replaces the topic list of one repository |

All three belong to the Actions administrator group, so they are listed only, never part of a starting profile, and
chosen with the profile `actions-admin` or by name in a `tools` list. All act on one repository inside the
connection's targets; a repository outside them is refused before a secret is read or GitHub is contacted,
without naming it.

`get` answers exactly the fields of its output schema and nothing else of the repository. GitHub leaves the merge
fields out for a token without write access; they are absent then. Description, homepage, and topics are untrusted
text of the repository owner; the homepage address is never fetched.

`update` accepts only the arguments of its input schema. At least one value is required, and
only the values given are sent in one `PATCH` request that is never retried; after a timeout, a server error,
or an unreadable answer the refusal says the change may have been applied. The name changes only through
`github.repositories.rename`; visibility, security settings, transfer, deletion, rulesets, and the Actions
settings are not reachable. Archiving makes the repository read-only, and unarchiving (`archived: false`) needs
the same administrator role. GitHub refusing a value, such as a `default_branch` that is no existing branch, is an
invalid request that names no text from GitHub. Giving `allow_merge_commit`, `allow_squash_merge`, and
`allow_rebase_merge` all as `false` is refused locally, before any request; the current state is not read for that check. The answer names
the settings the call gave with the values GitHub reports, and carries no `before` value, since the change is
the only request. A refusal for a missing right names the permission below, never text from GitHub.

`github.repository-topics.set` replaces the whole topic list in one `PUT` request that is never retried, with the same
uncertainty message as `update`. A list holds at most 20 distinct topics of lowercase letters, digits, and hyphens,
each starting with a letter or digit and at most 50 characters; an empty list removes all topics. Anything else is
refused before a secret is read. The answer lists the topics GitHub reports, untrusted text; a list that differs from
the one sent is an error that says the state is unclear.

The token rights are listed under [GitHub Actions](setup.md#github-actions), whose rule for a token of its own they follow.

## Repository traffic

| Tool | Does |
| --- | --- |
| `github.traffic-views.get` | reads the page views of a repository over the last 14 days |
| `github.traffic-clones.get` | reads the clones of a repository over the last 14 days |
| `github.traffic-paths.list` | lists the ten most visited paths of the last 14 days |
| `github.traffic-referrers.list` | lists the ten referrers with the most visits of the last 14 days |

All four read one fixed GitHub route for the repository the call names, and change nothing. The views and the
clones answer the totals `count` and `uniques` and one entry per span with `timestamp`, `count`, and `uniques`;
`per` is `day` (the default) or `week`, and any other value is refused before a request is sent. The paths
carry `path`, `title`, `count`, and `uniques`, the referrers `referrer`, `count`, and `uniques`. GitHub keeps
traffic for 14 days only and lists no more than the top 10 paths and referrers, so no older figure and no
longer list exists. The tools report aggregate numbers only, never an individual visitor.

Paths, titles, and referrers come from visitors: they are untrusted data, are cut (a path at 4096 characters,
a title and a referrer at 200), and are never fetched. GitHub shows traffic only to a token with push access to
the repository, so a refusal says that. The token rights are listed under
[Repositories, rulesets, and properties](setup.md#repositories-rulesets-and-properties). No setup profile ticks
these tools; list them in a connection's `tools`.

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
