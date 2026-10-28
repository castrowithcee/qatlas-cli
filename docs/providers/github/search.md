---
description: >
  Describes the search rules every GitHub search tool shares: the search terms, the cursor, the qualifiers the targets force, the user and organization search scope, and errors and rate limits.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# GitHub search

The search rules of the [GitHub provider](README.md).

`github.repositories.search`, `github.code.search`, `github.issues.search`, `github.pull-requests.search`,
`github.commits.search`, `github.users.search`, and `github.organizations.search` read GitHub's own search index
with `terms`: search terms and GitHub qualifiers, such as `language:go` or `is:merged`, never a `query` argument or
a route of an agent's own. [Issue search](issues.md#issue-search) and
[pull request search](pull-requests.md#pull-request-search) are described with their groups, the others under
search of [repositories](repositories.md#search), [code and commits](code.md#search), and
[users and organizations](accounts.md#search).

None of the seven takes a `repository`, `project`, or `owner` argument: they are narrowed by the connection's
targets as a whole, exactly like `github.stars.list`. They page through the
[cursor contract](README.md#cursor-contract), a cursor bound to the tool, the forced qualifiers, the exact `terms`,
and the filters set beside them, so a continuation never silently searches something else.

## Target scoping

`github.repositories.search`, `github.code.search`, `github.issues.search`, `github.pull-requests.search`, and
`github.commits.search` force a `repo:` or `org:`/`user:` qualifier ahead of `terms` from every target the
connection names, so the search never reaches beyond them:

| Target | Forces |
| --- | --- |
| `repos/OWNER/REPO` | `repo:OWNER/REPO` |
| `users/LOGIN` owner target | `user:LOGIN` |
| `orgs/LOGIN` owner target | `org:LOGIN` |
| `repos/OWNER/*` | refused with `invalid-request`: a repository pattern names no owner kind to force `org:` or `user:` with; add the owner as `users/LOGIN` or `orgs/LOGIN`, or list its repositories one by one, to search inside it |
| `users/LOGIN/projects/NUMBER` or `orgs/LOGIN/projects/NUMBER`, alone | refused with `invalid-request`: GitHub's search grammar has no qualifier for a project, and, exactly as a project pattern is not enough to stand for its owner when creating a project, a project target here forces nothing and never widens the search to its owner either; add a repository or an owner target, or use a connection without targets |
| a project target beside a repository or an owner target | the repository's or the owner's qualifier only; the project itself still forces nothing |
| several repository or owner targets | every qualifier they force, once per distinct owner; GitHub unions repeated `repo:` and `org:`/`user:` qualifiers of the same name, so the search reaches their combined targets and never beyond them |
| none | unscoped, whatever the token reaches |

Whatever mix of targets a connection names, a search that would still force no qualifier at all is always
refused this way: none of the five ever sends a request a target list could not narrow.

`github.users.search` and `github.organizations.search` reach GitHub's whole index unless the targets narrow
them, since a repository, a repository pattern, or a project target gives neither an account to narrow a
user or an organization search by:

| Targets | `github.users.search` | `github.organizations.search` |
| --- | --- | --- |
| none | unscoped, `type:user` only | unscoped, `type:org` only |
| exactly one `orgs/LOGIN` owner target | `type:user org:LOGIN`, its members only | refused: no qualifier restricts organization search to one target |
| exactly one `users/LOGIN` owner target | refused: a personal owner gives it no members to narrow by | refused |
| a repository or a project target, or several owner targets | refused | refused |

A term list that could escape a forced qualifier is refused with `invalid-request` before a secret is
resolved, wherever a target forces one: a positive `repo:`, `org:`, `user:`, or `owner:` qualifier in any
case, since GitHub unions a repeated qualifier of the same name with the one Qatlas forces and could widen
the search back open; the `OR` operator; and parentheses, since either could change which terms a forced
qualifier binds to. A negated qualifier, such as `-repo:`, only narrows further and stays allowed everywhere,
and `in:`, `fork:`, `is:`, and every other qualifier that does not pick an owner or a repository stay
unrestricted. A connection with neither targets nor [exclusions](README.md#targets) has no boundary to protect and
is not restricted.

## Exclusions

A connection with exclusions filters every returned row against them, whatever the terms. The search also adds
negated `-repo:`, `-org:`, and `-user:` qualifiers for the exclusions, owners first, up to a fixed limit; they only
spare GitHub from returning rows that the filter would drop, so the filter alone is the guarantee. A project
exclusion has no qualifier. A positive qualifier that names an excluded owner or repository is refused with
`invalid-request`. A connection with only exclusions forces no positive qualifier, so a search without targets
reaches everything not excluded, and `OR` and parentheses stay allowed. `total_count` is GitHub's own sum and still
counts excluded rows.

## Errors and rate limits

Search never answers `not-found`: an empty batch, not a refusal, is how GitHub reports that nothing matched.
`github.code.search` shares GitHub's separate, lower search rate limit; it and the other search tools report
GitHub's rate limit as `rate-limited`, naming the wait, through the same [status handling](README.md#errors) as every
other tool.
