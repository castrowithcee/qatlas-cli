---
description: >
  Describes the confirmed changes of GitHub project items and drafts: the item tools, field values, items and drafts, and batches with their partial results.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# GitHub project items

The item changes of the [GitHub projects](projects.md). Every tool takes `project` as described under
[the target of a call](README.md#the-target-of-a-call); a change needs the confirmation described under
[confirmed changes](README.md#confirmed-changes).

The changes of projects themselves are listed under [project lifecycle](projects.md#project-lifecycle).

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
under [project fields](projects.md#project-fields).

## Items and drafts

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
opens no second issue.

## Batches and partial results

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
