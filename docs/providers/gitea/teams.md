---
description: >
  Describes the Gitea and Forgejo team tools: listing, searching, reading, creating, changing, and deleting the teams
  of an organization, their members, their repositories, and their activity feeds. It covers how team IDs are bound to
  the targets by a proof read, the settings that can be changed, differences between the products, and the limits.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Teams

These tools act on the teams of an organization and the people in them. Team names, descriptions, and activity
texts in answers are untrusted data; members and activities are personal data. [Forgejo](../forgejo.md) names the
differences.

## Target binding

- A team belongs to an organization. `teams.list` (with `organization`), `teams.search`, and `teams.create` take the
  organization as a plain name, which a connection with targets must name as an `orgs/NAME` target; `users/NAME` and
  `repos/OWNER/REPO` do not allow it. The refusal comes before the token is read and does not name the value.
- Every other tool takes a team `id`, which names no organization. A connection whose targets name no `orgs/NAME`
  refuses these tools and `teams.list` without `organization` before the token is read. Otherwise the tool first
  reads the team (`GET /teams/{id}`) and checks that its organization is named as `orgs/NAME`. A team outside the
  targets ends as `not-found`, as if it did not exist, without a name. Without targets the proof read is omitted.
- A change is one proof read followed by exactly one change request, which is never repeated, also not after a server
  error or a timeout.
- `teams.get` is the proof read itself and shows the team only after the check.
- `teams.list` without `organization` reads the teams of the account behind the token and drops every row whose
  organization the targets do not name; a batch may be short or empty while `has_more` is true. The lists of one
  organization carry no `organization` member in their rows; they are bound by the organization argument.
- `team-activities.list` keeps only activities whose repository belongs to the team's organization and lies inside the
  targets. Without targets all activities the instance shows are kept.
- `username` of the member tools is one user name and is not checked against the targets; the answer concerns the
  team.

## Behavior

- `teams.create` and `teams.update` take `name`, `description` (at most 255 characters), `permission` (`read`,
  `write`, `admin`), `includes_all_repositories`, `can_create_org_repo`, `units`, and `units_map`. `units_map` gives an
  access level per unit (`repo.code`, `repo.issues`, ...), `units` is the deprecated list of unit keys that all get
  the level of `permission`. Unless `permission` is `admin`, which reaches every unit, `create` needs one of them; the
  instance fails with a server error otherwise, so the tool refuses before the token is read. `update` changes only the
  settings passed and needs at least one; the instance applies `includes_all_repositories` only together with
  `permission`.
- `teams.search` takes `q` and `include_desc` (the instance searches descriptions unless `false`). The instance answers
  with `{ok, data}`; a failed search is an error. It shows only the teams the token may see.
- `teams.delete` is final; its members lose the access the team gave. It is offered only to a connection whose `tools`
  list names it and is in no setup profile.
- `team-members.add` adds an existing user; no invitation by email address exists. `team-members.check` reports a 404
  as `false`, which also covers a user the token does not reach; any other refusal stays an error. Team members are
  personal data, in the class of `accounts.me`. `team-activities.list` takes `date` (`YYYY-MM-DD`) and is a data class
  of its own, shared with `org-activities.list`.
- Creating, changing, deleting, and adding or removing members need an owner of the organization (or a site
  administrator). Gitea shows a team's detail, members, and feed
  to a token that may read the team's metadata, which its visibility and the organization's decide; Forgejo to a team
  member or a site administrator only.
- `team-repositories.*` and `repository-teams.*` show and change the same assignment of teams to repositories from
  two sides. Only a repository of the team's own organization can be assigned. A change needs both targets: the
  repository (`repos/OWNER/REPO` or an owner target) and its organization as `orgs/NAME`; a connection that names
  only the repository, or names no organization, refuses before the token is read. `repository` is `OWNER/REPO`
  and, as with other repository tools, optional when the targets name exactly one.
- `team-repositories.*` take the team `id`. Every one of them, `check` and the changes included, first reads the team
  (also without targets), requires that its organization is named as `orgs/NAME` when targets exist, and refuses a
  repository whose owner is not that organization with `not-found` and without a name; the instance ignores the
  `{org}` segment and looks the repository up in the team's own organization, so the tool compares it itself.
  `team-repositories.list` keeps only repositories inside the targets; a batch may be short or empty while
  `has_more` is true.
- `repository-teams.*` take the team name (letters, digits, `_`, `.`, `-`, at most 255) and send no proof read: the
  repository's owner is the organization of every team they show or change. `repository-teams.list` returns the first
  100 teams (the instance does not page) and needs no row check.
- `*.check` reports a 404 as `false`, which also covers a team or repository the token does not reach; any other
  refusal stays an error. The instance answers a hit with the repository (`team-repositories.check`) or the team
  (`repository-teams.check`) object, which is not shown.
- `team-repositories.add` and `.remove` need a member of the team or an owner of the organization on Gitea and a
  member of the team (or site administrator) on Forgejo; `repository-teams.add` and `.remove` need an administrator of
  the repository, and the instance answers 422 when the team already has access (`add`) or has none (`remove`).
  A change is one request, never repeated, also not after a server error or a timeout.
- The team object carries `visibility` (`public`, `limited`, `private`) on Gitea and takes it as a setting; Forgejo
  has none and refuses it before the token is read.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `teams.list`, `teams.search`, `teams.get` | read | both |
| `teams.create` | create | both |
| `teams.update` | update | both |
| `teams.delete` | delete, listed only | both |
| `team-activities.list`, `team-members.list`, `team-members.check` | read | both |
| `team-members.add`, `team-members.remove` | update | both |
| `team-repositories.list`, `team-repositories.check`, `repository-teams.list`, `repository-teams.check` | read | both |
| `team-repositories.add`, `team-repositories.remove`, `repository-teams.add`, `repository-teams.remove` | update | both |
