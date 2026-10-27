---
description: >
  Describes the instance administration tools of the Gitea and Forgejo providers: the runners of the whole instance,
  the jobs waiting for a runner, the instance-wide lists of workflow runs, workflow jobs, and packages, the
  administration of user accounts, organizations and repositories created for them, the binding to
  connections without targets, the listed-only tools, and the product differences.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-27
---

# Instance administration

These tools act as an instance administrator on data of the whole instance, not of an owner or repository. They
hold for Gitea and Forgejo alike unless a line names one product. Forgejo also administers its quota groups and
rules; see [Quota administration](../forgejo/README.md#quota-administration).

| Tool | Effect | Provider |
| --- | --- | --- |
| `admin-runners.list`, `admin-runners.get` | read | both |
| `admin-runner-jobs.search` | read | Forgejo |
| `admin-workflow-runs.list`, `admin-workflow-jobs.list` | read | Gitea |
| `admin-packages.list` | read, Gitea 28 and later | Gitea |
| `admin-runners.update` | update | Gitea |
| `admin-runners.delete` | delete, listed only | both |
| `admin-users.list` | read | both |
| `admin-users.update` | update | both |
| `admin-users.rename`, `admin-users.delete` | update and delete, listed only | both |
| `admin-emails.list`, `admin-emails.search` | read | both |
| `admin-user-emails.list` | read | Forgejo |
| `admin-user-emails.delete` | delete, listed only | Forgejo |
| `admin-orgs.list` | read | both |
| `admin-orgs.create`, `admin-repositories.create` | create | both |
| `admin-unadopted.list` | read | both |
| `admin-unadopted.adopt` | create | both |
| `admin-unadopted.delete` | delete, listed only | both |

## Binding

Every tool is instance-wide and therefore reachable only by a connection without targets. A connection with
targets refuses the call as `invalid-request` before the credential is resolved and before any request; the
refusal does not name a target. All arguments are fixed-path filters; no argument chooses a path, URL, header, or
body.

## Runners

`admin-runners.list` and `admin-runners.get` read the runners of the whole instance, whatever level owns them, with
every member the product answers; names, descriptions, and labels are untrusted data. Forgejo pages the list and
filters it with `visible`; Gitea answers it at once, cut at 100 runners (`has_more` then is true), and filters it
with `disabled`. `admin-runners.get` refuses an answer for another runner as `invalid-provider-response`. The
runners of a repository, an organization, or the user behind the token are described in
[Actions administration](actions-admin.md).

`admin-runners.update` exists on Gitea only and sends `disabled` alone: it pauses a runner or resumes it, and
nothing else of the runner changes. `admin-runners.delete` removes the registration of a runner for good. It is
offered only by a connection whose `tools` list names it and is in no setup profile. Registering a runner and
registration tokens are not offered.

`admin-runner-jobs.search` exists on Forgejo only and lists the jobs of the instance that wait for a runner whose
labels match `labels`, at most 100 at once and without paging.

## Workflow runs, workflow jobs, and packages

`admin-workflow-runs.list` and `admin-workflow-jobs.list` exist on Gitea only and list the runs and jobs of all
repositories, one batch at a time, with the filters the descriptor names. Titles, branches, job names, and labels
are untrusted data. Forgejo lists runs and jobs of a repository only (see [Actions](actions.md)).

`admin-packages.list` exists on Gitea only and lists the packages of all owners, filtered by `type` and by name;
the packages of one owner are described in [Packages](packages.md).
Names and versions are untrusted data; an address with user info or a query is left out. Gitea 1.27 has no such
route, so the tool is refused there before its request and names the version it needs.

## Users

`admin-users.list` lists the accounts of the whole instance with the person members of the user object, as
[Users](users.md) describes them. Sign-in name, authentication source, last sign-in, and security state of an
account are neither returned nor available as a filter, since a filter would disclose them. The filters differ by
product: Gitea filters by keyword, visibility, and the flags `is_active`, `is_admin`, and `is_restricted` and sorts by
attribute and order; Forgejo only sorts.

`admin-users.update` locks or unlocks an account (`prohibit_login`, `active`), restricts it, and changes its limits,
visibility, and profile texts; only the given members are sent, and `hide_email` and `pronouns` exist on Forgejo
only. It never sends what hands over an account (password, email address, sign-in name, authentication source,
forced password change) or raises it to the host (administrator right, Git hooks, local import). Gitea documents
`source_id` as required but keeps the source when it is absent, so the tool omits it. An answer for another user
than the requested one is refused as `invalid-provider-response`.

`admin-users.rename` renames an account. `admin-users.delete` removes it for good; with `purge`, the instance also
removes everything the account owns. Both are offered only by a connection whose `tools` list names them and are in
no setup profile. Creating an account is not offered.

### Email addresses

`admin-emails.list` and `admin-emails.search` list the email addresses of all accounts with every member of the
email object; `q` of the search is optional. `admin-user-emails.list` lists the addresses of one account, without
paging and capped. `admin-user-emails.delete` removes up to 20 distinct addresses of one account in one request; it
is offered only by a connection whose `tools` list names it and is in no setup profile. Adding an address is not
offered. The two tools of `admin-user-emails` exist on Forgejo only.

### Organizations and repositories

`admin-orgs.list` lists all organizations of the instance, hidden ones included, with the members of
[Organizations](organizations.md). `admin-orgs.create` and `admin-repositories.create` create an organization or a
repository for the existing account `username`; the settings, limits, and exclusions are those of `orgs.create` and
`repositories.create` (see [Repositories](repositories.md)), except that the owner is `username` and a new
organization is named by `organization`. Both are not idempotent, are in no setup profile, and are refused as
`invalid-provider-response` when the answer names another organization or repository.

### Unadopted repositories

`admin-unadopted.list` lists one batch of the repository directories on the disk that no repository record owns, as
`owner`, `repo`, and `full_name`; an entry that is not exactly one valid `owner/repo` pair is left out. `pattern`
is an optional glob of the form `owner` or `owner/repo` with at most one `/`; the tool sends it under the name the
instance version knows (`query` on Gitea 28 and later, `pattern` otherwise). `admin-unadopted.adopt` registers the
files of one directory as a repository and is not idempotent. `admin-unadopted.delete` removes the files of one
directory for good; it is offered only by a connection whose `tools` list names it. Both take `owner` and `repo`,
are in no setup profile, and are refused for any other name.

## Changes

`admin-runners.update`, `admin-runners.delete`, and the tools of `admin-users`, `admin-user-emails`, `admin-orgs`,
`admin-repositories`, and `admin-unadopted` that change need confirmation, send exactly one request, and are never
repeated; when the outcome is open, the message says that the change may have been applied. No tool of this group is
in a setup profile.
