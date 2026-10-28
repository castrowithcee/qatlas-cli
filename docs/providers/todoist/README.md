---
description: >
  Describes the Todoist provider: setup with a personal API token, project and account scope, target choice, the cursor contract, plan-dependent errors, and the boundary to workspace and account administration; the tools, changes, and filters are on their own pages.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-10-26
---

# Todoist

Todoist is a provider for one personal Todoist account through the official Todoist API v1. It reads the account's projects, sections, labels, tasks, comments, reminders, and saved
filters, and changes tasks, comments, time-based reminders, personal projects, sections, and personal labels, each
within the connection's scope; every change needs its own confirmation. Saved filters are only read. [Set up
Todoist](setup.md) leads through creating the token, checking the connection, and fixing errors.

Its tools are grouped, each group on its own page; this page holds what applies to all of them.

| Page | Covers |
| --- | --- |
| [tools.md](tools.md) | the read and change tools, their effects, and the setup profiles |
| [changes.md](changes.md) | confirmation, placement, retry contract, and audit of every change |
| [filters.md](filters.md) | structured and expression filters of task reads |

## Configuration

A service is always the official API root `https://api.todoist.com/api/v1`, which the terminal editor fills
in and which applies when `base_url` is left out. Qatlas refuses any other URL before a secret is read,
because a personal token belongs to that one service.

The credential provides `token`, the personal API token of the account. That token reaches the whole account, so the
connection target is what limits Qatlas. How to get the token, what the account's plan and roles allow, and how to
test the connection are described in [Set up Todoist](setup.md).

```yaml
services:
  todoist:
    provider: todoist
    base_url: https://api.todoist.com/api/v1

credentials:
  todoist-reader:
    provider: todoist
    type: keyring
```

## Scope

A connection names its targets explicitly, or deliberately the whole account. Several targets form an
allow-list: the connection reaches what any of them reaches.

| Target | Binds |
| --- | --- |
| `PROJECT_ID` | exactly this project, without its subprojects |
| `project/PROJECT_ID` | this project and all its subprojects at any depth, also those created later |
| `section/SECTION_ID` | the tasks of this section, with their comments and reminders |
| `*` | the whole account, including saved filters; stands alone |

An ID is the ID the matching list tool returns (letters and digits). Subprojects are resolved on every
call from the project list, so no subproject is remembered across calls. An empty target never means the
whole account. The terminal editor warns whenever `*` is entered.

A binding reaches a tool only where the tool can act inside it; the others are not offered, and the call
check stays in force:

| Tools | `*` | `PROJECT_ID`, `project/PROJECT_ID` | `section/SECTION_ID` only |
| --- | --- | --- | --- |
| task, completed-task, comment, and reminder tools; `todoist.labels.list` | yes | yes | yes, within the section |
| `todoist.sections.get`, `todoist.sections.update` | yes | yes | yes, the bound section |
| project tools except create; section list, create, reorder, move, delete | yes | yes | no |
| `todoist.projects.create` | yes | only below a `project/PROJECT_ID` project | no |
| label changes, `todoist.filters.list` | yes | no | no |

No argument names or widens the scope. A `project_id`, `section_id`, or `parent_id` argument only selects
inside it and is refused when it lies outside. Every object ID is proven against the scope before it is
read, changed, or returned, and no refusal names the object or a target. A task belongs to a section
binding when its `section_id` is the bound one. The comments and reminders of a task are requested only
after the task was read and found inside. Todoist answers are checked again: a row outside the scope is never
returned, even when Todoist answers more broadly than asked. A connection bound to exactly one project or
one section narrows every task request to it at Todoist; a project tree is narrowed only by a named
`project_id`.

A change reads what it concerns first and is refused before anything is sent when it lies outside the
connection: no task is created in, moved into, or attached below another place, a section must belong to the
scope, and a comment is changed only when its task or project belongs to it. A new task without a place goes
to the connection's only target. A `*` connection reads nothing first.

Structure changes follow the same boundary:

- A new project would lie outside every list unless it joins a tree, so `todoist.projects.create` needs
  `parent_id` inside a `project/PROJECT_ID` project on any binding but `*`; it joins that tree at once.
- Todoist archives and deletes a project with all its subprojects. Qatlas reads the project tree first (for a
  delete the archived projects too) and refuses the change when a subproject at any depth lies outside the
  connection. A tree of more than 25 pages of 200 projects is refused unresolved. Unarchiving restores the
  project alone, as a top-level project.
- Only personal projects are changed: every project change reads the project first and refuses a workspace
  project, and no project is created in or below one.
- A reminder belongs to a task, which is read first and must belong to the connection.

## Choosing targets

A person chooses from the projects of the account, subprojects below their project with the parent chain as
path, and then from the sections of one project. A chosen project becomes `project/PROJECT_ID`, a section
`section/SECTION_ID`; an exact `PROJECT_ID` is entered by hand. The connection's targets do not narrow what is
offered, and choosing only reads, through no tool and for no agent. Projects and sections are searched by name
at Todoist, a project search at any depth; names are untrusted and are shortened and cleaned.

| URL | Target |
| --- | --- |
| `app.todoist.com/app/project/SLUG-ID` | `project/ID` |
| `app.todoist.com/app/task/SLUG-ID` | the task's `project/ID` and, if set, its `section/ID`, after one read of the task |

The ID follows the last `-` of the last segment. Any other form or host resolves to nothing.

## Cursor contract

Every list except the saved filters takes `limit` (1 to 200, default 50) and an opaque `cursor`, and
answers `has_more` plus `next_cursor` whenever Todoist announced a further page. Each request reads exactly
one Todoist page; Qatlas never loads a following page on its own. Because scope and due window are applied
to that page, a batch may be short or even empty while `has_more` stays true: read on until `has_more` is
false. A full batch never means the end.

The cursor wraps Todoist's own cursor unchanged. It is bound to the tool, the connection's scope, and the
arguments that produced it, and it keeps the batch size of its first batch; a cursor from other arguments,
another tool, or another connection is an invalid request. Todoist cursors are short-lived: when Todoist no
longer accepts one, start the list again without a cursor. Tasks that change while a caller pages can
appear twice or move past the cursor.

`todoist.filters.list` answers every saved filter at once through one read-only Sync request that asks for
filters only and carries no command. Todoist counts it against its stricter Sync request budget.

## Errors and plans

Errors keep stable classes; the message carries no token, Todoist text, or URL query ([answer](setup.md#fixing-errors)):

| Class | Cause |
| --- | --- |
| `auth` | Todoist rejected the token |
| `permission` | the account's plan does not include the feature, or the token may not read or change the resource; the message tells them apart (see [Fixing errors](setup.md#fixing-errors)) |
| `rate-limited` | Todoist asked to wait; the next request of the same token waits as asked, at most one minute |
| `not-found` | Todoist does not hold the resource or does not show it to this token, or it was deleted |
| `provider-error` | Todoist rejected the request, no longer accepts the cursor, or answered a change with a server error |
| `invalid-provider-response` | the answer was unreadable, too large, or lacked identifiers |

A resource outside the connection's projects is an invalid request, never a provider error, so a scope
refusal cannot be mistaken for a missing resource. A change whose outcome is unclear says so in its message,
whatever its class.

## Untrusted data

Names, contents, descriptions, comments, and filter queries come from the account and are untrusted data.
Qatlas passes them on as JSON strings and never renders Markdown or HTML, follows a link, or runs a filter
query unless a caller passes it to `todoist.tasks.filter`.

## Boundary

The provider reads a personal account and changes its tasks, comments, reminders, personal projects,
sections, and personal labels. It does not change saved filters, shared labels, location reminders, or
workspace projects, uploads no files, offers no bulk changes, does not administer workspaces, folders,
collaborators, invitations, users, billing, account, view, or notification settings, or OAuth apps, does not
read live notifications, activity, backups, or archived projects, offers no generic Sync or API passthrough,
and keeps no local copy of the account. Tasks and sections of workspace projects the account has joined are
read and changed like any other project when the connection names them; the workspace project itself is not.

Todoist marks `note_count` as deprecated and always answers 0, so a task has no comment count; its comments
come from `todoist.comments.list`. `is_deleted` is not returned: Qatlas drops every deleted entry, so the value
would always be false. Attachment addresses hosted by Todoist are not returned: its documentation does not show
that they require a sign-in, so they count as an access value, and no Todoist tool returns one. External
addresses appear as untrusted text and are never fetched. The parameter `public_key` of `GET /tasks/{task_id}`,
`GET /tasks/completed/by_completion_date`, `GET /sections`, `GET /sections/{section_id}`, and `GET /comments` is
not offered: it reads publicly shared projects of others, outside the connection's targets. A project's own
`public_key` grants access without a sign-in; as an access value it is not returned.
