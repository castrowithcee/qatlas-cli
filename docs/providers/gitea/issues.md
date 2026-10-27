---
description: >
  Describes the issue tools shared by the Gitea and Forgejo providers: reading, creating, changing, closing, and
  deleting issues, due dates, timelines, issue templates, and the issue configuration, with target binding, bounds,
  and product differences.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Issues

Every issue tool takes `repository` (`OWNER/REPO`, checked against the targets like any repository argument) and,
for one issue, its `number`. Issues and pull requests share one index space.

- `issues.get`, `issues.update`, `issues.close`, `issues.reopen`, `issues.delete`, and `issue-deadlines.set` first
  read the issue. A pull request is refused as `invalid-request` with a pointer to the pull-request tools, before
  anything is changed, and so is an answer whose canonical repository lies outside the targets. `issues.list` and
  `issues.search` ask for issues only. `issue-timeline.list` also reads the timeline of a pull request.
- `issues.search` reads across repositories; every row is checked against the targets by the repository it reports,
  and a row without one is dropped, so a batch may hold fewer issues than the instance sent. `owner` must lie inside
  the targets. The server-side filters are only an optimization.
- Assignees are the `assignees` member of `issues.create` and `issues.update`; on an update the list replaces the
  current assignees and an empty list removes them. The user objects of authors and assignees are personal data.
- `issues.update` never changes the state and not the due date; `issues.close` and `issues.reopen` set the state,
  `issue-deadlines.set` sets the due date or, with an empty `due_date`, removes it. An issue that already has the
  requested state is returned with `changed` false and nothing is sent.
- `issues.delete` removes the issue and its comments for good. It is offered only by a connection whose `tools` list
  names it and is in no setup profile.
- The timeline shows another issue, pull request, or comment an event refers to by identifiers only. A reference
  event (`issue_ref`, `comment_ref`, `pull_ref`) whose source repository lies outside the targets, or names none,
  is left out, as for an instance user without read access to that repository; other event types are unchanged.
  Without targets everything is shown.
- Not offered: the deprecated `assignee` of create and update, and the due-date members of both, which
  `issue-deadlines.set` covers. A label is set on creation only, by its numeric ID; the update API has no label member.

## Issue templates and configuration

`issue-templates.list`, `issue-config.get`, and `issue-config.validate` read the issue templates, the issue
configuration file, and the instance's validation of it for one repository (`repository` as `OWNER/REPO`, checked
against the targets). Template and link texts, form attributes, and validation messages come from repository files
and are untrusted data. `assignees` of a template is a Gitea member. The template list is cut at 100 entries with
`truncated` set.

The body of an issue and the text of a timeline event are cut at 64 KiB.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issues.list`, `issues.search`, `issues.get`, `issue-timeline.list` | read | both |
| `issue-templates.list`, `issue-config.get`, `issue-config.validate` | read | both |
| `issues.create` | create | both |
| `issues.update`, `issues.close`, `issues.reopen`, `issue-deadlines.set` | update | both |
| `issues.delete` | delete, listed only | both |

## Gitea

- Gitea has no `sort` on issue lists and searches.
- An issue carries `content_version`, `time_estimate` (read only), and `projects`. `issues.update` takes
  `content_version`, the version read before, to refuse an update after the title or body changed, and `projects`,
  which replaces the projects; `issues.create` takes `projects`. `issues.search` takes `created_by`.
