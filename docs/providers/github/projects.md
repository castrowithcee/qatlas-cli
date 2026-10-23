---
description: >
  Describes the GitHub project tools: the owner project list, the project lifecycle and templates, the project field schema, views, status updates, collaborators and teams, built-in automations, project-first work selection, and the confirmed changes of items and drafts with their batches and partial results.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub projects

The project tools of the [GitHub provider](README.md). Every tool takes `project` as described under
[the target of a call](README.md#the-target-of-a-call); a change needs the confirmation described under
[confirmed changes](README.md#confirmed-changes).

## Owners

`github.projects.list` finds a project number without knowing it in advance. It lists the projects of an owner
(`users/LOGIN` or `orgs/LOGIN`), open and closed, in number order unless `order_by` and `direction` say otherwise;
`query` searches them and `min_permission_level` keeps those the token holds at least that permission on. Its `project`
value is what the other tools take as `project`. The targets decide what it shows: [owner lists](README.md#owner-lists).

Reading the projects needs `read:project` on a classic token. A fine-grained token lists the projects of an organization
with Projects read access, but not the projects of a user; GitHub refuses that with `permission`.

The tools that change a project, its fields, views, status updates, collaborators, and items need `project` on a classic
token, or Projects read and write access of the organization on a fine-grained token; the projects of a user need a
classic token. The sections below name only what a tool needs beyond that.

The read tools on this page report every member GitHub documents for their objects, except rendered variants, which
repeat the text in another format and add no content, deprecated members a documented one replaces, lookups by argument
(`field`, `view`, `workflow`, `projectV2`), which are queries, and the object a call names, which the call fixes and no
entry repeats (such as the project of a field or the organization of a team).

A connection GitHub also serves as a list of its own is read with that list and reported as a count; any other list
inside an entry holds its first 10 entries, with its total and a truncated marker. A nested object carries its own
fields, and an account the fields GitHub's own REST answers give it; the full profile is read with the account tools. A
flat value that was there before (`author`, a name list) stays, with the full object beside it as `<name>_detail` or
`<name>_details`. Names, titles, descriptions, bodies, filters, and addresses are untrusted data; a readme is cut at
4000 characters, and a linked repository shows its address only inside the targets. A member of a project or team that
GitHub refuses the token is absent and named in `unreadable`; the team count of a project is read only with
`team_counts`.

## Project lifecycle

The lifecycle tools make and maintain projects themselves, not their items. Every change follows the rules under [confirmed changes](README.md#confirmed-changes).

| Tool | Does |
| --- | --- |
| `github.projects.create` | creates one empty project in `owner` |
| `github.projects.update` | changes the settings of a project; left-out settings stay |
| `github.projects.copy` | copies `project` with its fields and views into a new project of `owner`; its draft issues can be copied as well |
| `github.projects.link` | links `project` to `repository`; a linked project stays linked once |
| `github.projects.unlink` | removes the link between `project` and `repository` |
| `github.project-templates.mark` | marks `project` of an organization as a template |
| `github.project-templates.unmark` | stops offering `project` as a template |
| `github.projects.delete` | deletes `project` with its items, fields, and views; listed only |

`closed` closes or reopens a project, and `public` switches its visibility, which an organization policy may
forbid. At least one setting is required. `short_description` and `readme` need at least one character: GitHub
ignores an empty value and answers with success, so it cannot clear either through its API, and Qatlas refuses
`""` before a secret is read. A repeated link or unlink succeeds again and leaves the project linked once or
unlinked. A repeated delete ends as `not-found` when the project is resolved, before any mutation; its
idempotency stays `unknown` because a delete addresses the project by number: read the current state before
repeating one.

A template is a project its organization offers when someone creates a project; `github.projects.copy` copies
any allowed project, template or not. The template tools answer the project's state with `template`. GitHub
marks only projects of an organization as templates: it refuses a user's project as unprocessable, so
Qatlas refuses `github.project-templates.mark` for a `users/LOGIN/projects/NUMBER` project with
`invalid-request` before a secret is read. Unmarking a project that is no template, a user's project
included, succeeds and changes nothing.

`github.projects.create` and the destination of `github.projects.copy` need the owner as an owner entry of
the targets, or a connection without targets; `owner` may be left out when the targets name exactly one owner
entry. Every other lifecycle tool, and the source of a copy, needs the project inside the targets like any
project tool, and a link or an unlink needs the repository inside them as well. `github.projects.delete`
needs its name in the connection's `tools`:

```yaml
connections:
  projects:
    service: github
    credential: github-planner
    targets: [orgs/octo-org, orgs/octo-org/projects/*, repos/octo-org/*]
    tools: [github.projects.list, github.repositories.list, github.projects.create, github.projects.update,
      github.projects.copy, github.projects.link, github.projects.unlink, github.projects.delete]
```

A link or an unlink also needs a token that can see the repository. Deleting a project needs the rights of a project administrator. A copy takes
the views of its source along. No lifecycle tool deletes a field, an option, a view, or an item; the
[fields](#project-fields) and the [views](#project-views) have tools of their own.

## Project fields

The field schema tools read the fields of a project and create, change, and delete them. The list reports the
identifiers of fields, options, and iterations; every tool takes them by name only, compared without case, and resolves
every name against the project's fields in one query before the one change is sent.

| Tool | Does |
| --- | --- |
| `github.project-fields.list` | lists every field with its type, options, and iterations, completed ones also apart |
| `github.project-fields.create` | creates one field |
| `github.project-fields.update` | renames a field or changes its options; every value stays |
| `github.project-field-options.delete` | removes named options; every item loses a removed option; listed only |
| `github.project-iterations.replace` | changes the iteration settings and adds, changes, or removes iterations; every item loses its value of the field; listed only |
| `github.project-fields.delete` | deletes a field that is not built in, with its values; listed only |

Built-in fields cannot be created or deleted. The `Status` field every project has is built in as well: GitHub neither
deletes nor renames it, but its options are maintained like those of any single-select field. A field that mirrors an
organization issue field names it; creating or changing issue fields is not supported.

`github.project-fields.create` refuses a name the project already holds before the change, so a repeated create never
makes a second field of one name.

GitHub replaces the options of a field as a whole, so `github.project-fields.update` sends every option the field keeps
together with its identifier: renamed, recolored, and reordered options keep the values items hold, and an option left
out stays as it is.

The value-losing changes are separate tools with the effect `delete`, listed only:

- `github.project-field-options.delete` removes the named `options` of a select field; at least one option stays, and
  deleting the field removes the last one.
- `github.project-iterations.replace` changes the iterations of an iteration field; an iteration is named by its
  `title`, and one the field lacks is added and then needs a `start_date`. Iterations left out stay, completed ones
  included. GitHub takes iterations without identifiers and recreates every iteration of the field with such a change,
  so every item loses its value of this field, whichever iterations the change names; set the values again afterwards.
  Renaming an iteration field through `github.project-fields.update` keeps its values.

A field, option, or iteration the project lacks, an option or iteration named twice, and an `order` that leaves an
option out are refused before any change and name what the field holds. A repeated removal or delete is refused that way
once the name is gone; its idempotency stays `unknown`, because a name may be taken again in between.

## Project views

The view tools read the views of a project and create, change, and delete them. A view is addressed by `view`, its
number in the project, and the fields it shows by their names, compared without case and resolved against the project's
fields and views in one query before the one change is sent.

| Tool | Does |
| --- | --- |
| `github.project-views.list` | lists every view |
| `github.project-views.create` | creates one view |
| `github.project-views.update` | changes a view; left-out settings stay |
| `github.project-views.delete` | deletes one view; the items and fields stay; listed only |

`github.project-views.list` answers at most 100 views. Each name list (`fields`, `group_by`, `column_by`, `sort_by`)
stays, with the same fields as objects with identifiers beside it.

A roadmap takes no visible fields: GitHub refuses them, so Qatlas refuses `fields` for a roadmap, new or existing,
before any change. A new board without further settings takes its columns from `Status`; a board changed to another
layout no longer reports `column_by`.

`filter` is written in GitHub's project filter syntax, such as `status:Todo` or `label:bug -status:Done`. It is the only
filter expression Qatlas takes from a caller, and it reaches GitHub as one value, never as part of a query. GitHub
judges the syntax and keeps a filter it cannot read as written.

GitHub creates a view without a filter, so `github.project-views.create` with a non-empty `filter` sends two changes:
the create, and then the filter of the new view. Once the view exists, the answer reports it even when the filter could
not be set or may not have been, with `complete` false and `error`; set the filter with `github.project-views.update`
then, instead of creating the view again. A repeated create makes a second view.

A view number the project lacks is refused before any change and names the project's view numbers, and an unknown field
names the project's fields. GitHub keeps the last view of a project, so Qatlas refuses to delete it. A repeated delete
is refused once the number is gone; its idempotency stays `unknown`, because a delete addresses the view by number.

GitHub's API does not set the grouping, the sorting, the board column field, field widths, the slice, or field sums of a
view. The list reads the first three as GitHub reports them; widths, slices, and sums are neither read nor set. Change
them in GitHub itself.

## Project status updates

The status update tools read the status updates of a project and post, change, and delete them. A status update is
addressed by `status_update_id`, the `id` the list names; it is resolved together with the project in one query and must
belong to it before the one change is sent.

| Tool | Does |
| --- | --- |
| `github.project-status.list` | lists the status updates, newest first, in batches |
| `github.project-status.create` | posts one status update |
| `github.project-status.update` | changes a status update; left-out settings stay |
| `github.project-status.delete` | deletes one status update; listed only |

`github.project-status.list` follows the [cursor contract](README.md#cursor-contract) with a cursor bound to the
project. `author` stays the login, with the author as an account beside it.

A create needs at least one setting; GitHub posts a status update without a status or dates as well. GitHub does not
check that the target date follows the start date. A status update of another project, or one GitHub does not show, is
`not-found` before any change; a repeated delete ends that way.

## Project collaborators and teams

The access tools read the teams a project is linked to and change who reaches it. The changes are listed only: a
connection offers them only while its `tools` list names them, and no setup profile ticks them.

| Tool | Does |
| --- | --- |
| `github.project-teams.list` | lists the teams the project is linked to, in name order, in batches |
| `github.project-collaborators.update` | grants teams and members of the owning organization a role, changes it, or removes direct access; listed only |
| `github.projects.link-team` | links a project of an organization to one of its teams, which GitHub grants read access; listed only |
| `github.projects.unlink-team` | removes that link and the read access it granted; listed only |

`github.project-teams.list` follows the [cursor contract](README.md#cursor-contract) with a cursor bound to the project.
The `team` slug of an entry is what `github.projects.link-team` takes. A team reports its settings, its parent, and its
first 10 ancestors, member statuses (each with its user as an account), and linked projects; its members, child teams,
repositories, and invitations are counted only. A project of a user is linked to no team and lists none. GitHub shows
teams only to a token that may read the organizations, even for a project of a user: without `read:org` on a classic
token, or Members read access on a fine-grained one, the list ends with `permission`, and the message names that scope.

Collaborators left out stay unchanged. Qatlas grants no access to people outside the owning organization: a role other
than `none` for a `user` needs a project of an organization and a user who is a member of it, which Qatlas checks live
before the change. Only a confirmed membership counts; a missing or unreadable membership refuses the whole request with
`invalid-request` before any change, and the message does not name the login. A token that may not read memberships ends
with `permission`. `none` is allowed for every user and team, also on a project of a user. Every user and team is
resolved with the project in one query first: an unknown login or a team the organization lacks is `not-found` before
any change. A team is looked up inside the project's organization only, so a team of another organization can never be
named.

Teams belong to an organization, so Qatlas refuses a team for a `users/LOGIN/projects/NUMBER` project, in
`github.project-collaborators.update` and in the team links, with `invalid-request` before a secret is read. GitHub's
API reads neither the collaborators of a project nor their roles, so there is no tool that lists them; check them in
GitHub itself. Changing collaborators needs the rights of a project administrator; resolving a team or checking a
membership also needs `read:org` on a classic token, or Members read access on a fine-grained one. Inviting people by
email, managing the teams themselves, and the settings of an organization are out of scope.

## Project automations

The automation tools read the built-in workflows of a project, such as `Item closed`, and delete one. A workflow is
addressed by `workflow`, its number in the project, and resolved with the project in one query before the change is sent.

| Tool | Does |
| --- | --- |
| `github.project-workflows.list` | lists every workflow, in number order |
| `github.project-workflows.delete` | deletes one workflow; listed only |

GitHub's API neither creates, enables, disables, nor configures a workflow, and a deleted workflow cannot be created
again through it: change them in GitHub itself. A workflow number the project lacks is refused before any change and
names the project's workflow numbers; a repeated delete ends that way. Its idempotency stays `unknown`, because a delete
addresses the workflow by number.

## Project-first use

When a repository has an authoritative project, work selection starts there. `github.project-items.list` reads
a batch of items with their content and field values: without `status` and `status_not` at most 30 items whose
Status is not `Done`, with `status_not: []` every status. Its filters become quoted single terms of the project
filter syntax, a field name with spaces written with hyphens like GitHub's filter keys; GitHub applies them, and
Qatlas verifies status, type, repository, assignee, and labels on each returned item. A `status` value must be
an option of the project's Status field. GitHub indexes the list with a delay, so a new item can be missing for
a few seconds; `github.project-items.get` reads it by `item_id`. An item reports its content's own fields
without rendered variants or comments, which `github.comments.list` and `github.pull-request-comments.list`
read. Under GitHub's node limit a list inside a field value holds its first 5 entries, marked when cut, and a
repository there outside the targets only its identifiers; the list cuts bodies at 1000 characters, the detail
at 4000. Bodies and titles are untrusted data.

## Item changes

The changes of projects themselves are listed under [project lifecycle](#project-lifecycle).

| Tool | Does |
| --- | --- |
| `github.project-items.update` | sets or clears field values of one item |
| `github.project-items.add` | adds an existing issue of a named repository, then sets fields |
| `github.project-items.archive` | archives one item; GitHub keeps it restorable |
| `github.project-items.unarchive` | restores one archived item |
| `github.project-items.move` | places one item after another, or first |
| `github.project-items.delete` | removes one item from the project; an issue or pull request stays, a draft is deleted; listed only |
| `github.project-drafts.create` | adds one draft issue, then sets fields |
| `github.project-drafts.update` | replaces the title, body, or assignees of a draft; left-out fields stay |
| `github.project-drafts.convert` | turns a draft into an issue of a named repository; the item keeps its identifier, place, and fields |
| `github.project-issues.create` | opens an issue in a named repository, adds it, then sets fields |

Project field values are named, not identified: names are compared without case, and a multi-select list
replaces the selected options. Title, assignees, labels, and other built-in fields are not set through
`fields`. At most 20 values are accepted per request. The fields a project holds and their options are listed
under [project fields](#project-fields).

### Items and drafts

The item tools address an item by the `item_id` of `github.project-items.list`, resolve the project and every
item they name in one query, and refuse an item of another project, or one GitHub does not show, as `not-found`
before any change, `after_id` of `github.project-items.move` included. `github.project-items.move` sets the
project order, which views without a sort show: directly after `after_id`, or first without it.
`github.project-items.delete` removes the item and its field values; an issue or pull request stays in its
repository, a draft issue exists only as its item and goes with it. A repeated delete finds no item and is
refused, and an issue added again gets a new item, so a repetition never removes anything else.
`github.project-items.archive` reads whether the item is archived with the project and answers an archived item
with `archived: true` without a change, so a repeated archive succeeds like the first.

`github.project-drafts.update` and `github.project-drafts.convert` accept only the item of a draft issue and
refuse an issue or pull request as `invalid-request`. `assignees` names logins and replaces every assignee; `[]`
removes them all, an unknown login is `not-found`, and `body: ""` empties the body. `github.project-drafts.convert`
creates the issue in `repository`, which the targets must allow beside the project, and answers with its number
and URL. The item keeps its identifier, place, and field values, so a repeated conversion finds no draft and
opens no second issue. The token needs write access to the issues of that repository besides the project.

### Batches and partial results

A project change reads the project, its fields, their options and iterations, and the item or issue it
concerns in one query, and resolves every field value before the first change is sent. An unknown field,
option, or iteration, a value of the wrong type, or an item of another project is refused without any
change. The values are then written in field-name order in serial GraphQL requests of at most 10 aliased
mutations each. Adding an item and setting its fields are separate requests, because GitHub does not allow
both in one; `github.project-issues.create` creates the issue, adds it, and sets its fields in three steps.

The answer lists every field with its `result`: `updated`, `failed`, `unknown` when the value may have been
written without a confirmation, or `not_sent` when an earlier failure stopped the request. A failure ends the
request after its batch. `complete` is true only when every step succeeded; otherwise `error` says what
stopped it. `github.project-items.update` fails as a whole only when no value was or may have been written.
Once an issue, a draft, or an item exists, the answer always reports it, including an issue that was created
but could not be added to the project, so a caller never creates it a second time to learn the outcome.
