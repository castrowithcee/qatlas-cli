---
description: >
  Describes read-only GitHub project and issue access, project-first planning, targets, the cursor contract, and token scopes.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-09-23
---

# GitHub

GitHub is a controlled planning provider, not a replacement for `gh`. It only reads: it never writes, never
reads comments, sees pull requests only as project items, and never accepts a free filter expression, a
GraphQL or REST path, an owner, a repository, or a project from the caller.

## Configuration

A service is `https://api.github.com` (the default), `https://api.SUBDOMAIN.ghe.com` for GitHub Enterprise
Cloud with data residency, or `https://HOST/api/v3` for GitHub Enterprise Server. Qatlas derives the GraphQL
endpoint from it (`/graphql`, or `https://HOST/api/graphql` on Enterprise Server). Filtered project items
need a server that supports the `query` argument of `ProjectV2.items`.

The credential provides `token`, a personal access token. A read-only setup uses a classic token with
`read:project` plus `repo` (or `public_repo` for public repositories only), or a fine-grained token with read
access to issues and to projects. User-owned projects need a classic token. A successful
`qatlas connection test` shows only that the token can read the configured target; GitHub checks every
resource and scope again on each call, so a passing test does not authorize every tool.

Every connection needs exactly one `target`:

| Target | Binds | Tools |
| --- | --- | --- |
| `users/LOGIN/projects/NUMBER` | one user project | `github.projectitems.list`, `github.projectitems.get` |
| `orgs/LOGIN/projects/NUMBER` | one organization project | `github.projectitems.list`, `github.projectitems.get` |
| `repos/OWNER/REPO` | one repository | `github.issues.list`, `github.issues.get` |

A tool of the other target kind is refused as an unsupported capability before a secret is read. Give each
connection a `tools` list with the tools of its kind, so discovery offers only the tools it can run.

## Project-first use

When a repository has an authoritative project, work selection starts there:

```sh
qatlas invoke github.projectitems.list --connection planning
echo '{"status":["In progress"],"type":"issue"}' | qatlas invoke github.projectitems.list --connection planning
qatlas invoke github.projectitems.get --connection planning --arg item_id=PVTI_...
```

`github.projectitems.list` returns compact items: identifier, type, title, number, repository, state, Status,
the other single-select, text, number, date, and iteration values by field name, assignees, labels, and URL.
It never returns bodies or comments. Without `status` and `status_not` it lists at most 30 items whose Status
is not `Done`; `status_not: []` lists every status. The filters `status`, `status_not`, `type`
(`issue`, `pull_request`, `draft_issue`), `repository` (`owner/name`), `assignee`, and `labels` (any of) are
translated into quoted terms of the project filter syntax and applied by GitHub. Qatlas verifies each
returned item against the same filters. A `status` value must be an option of the project's Status field.

`github.projectitems.get` reads one item of the bound project with its fields and, for an issue or a draft
issue, the full body. An item of another project is refused. Bodies and titles are untrusted data.

`github.issues.list` and `github.issues.get` are the fallback for a repository connection: issues only,
newest first, filtered by `state` (`open` by default), `labels` (any of), and `assignee`, without comments.

## Cursor contract

Lists take `limit` (1 to 100, default 30) and an opaque `cursor`, and answer `has_more` plus `next_cursor`
when another batch may follow. A full batch never means the end: read on while `has_more` is true. A cursor
is bound to the connection's target and to the filters that produced it; a cursor from other filters or
another target is an invalid request. Batches follow the project order, so reading every batch reaches each
matching item once. A batch may be short, even empty, when Qatlas stopped scanning after a bounded number of
requests; `has_more` then stays true.
