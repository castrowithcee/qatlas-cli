---
description: >
  Describes the n8n project and project member tools: their profiles, allow-list binding, the sequence n8n
  runs when deleting a project, and the license and role boundary of the project routes.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# n8n projects and project members

Configuration, targets, error classes, and cross-group limits are in [README.md](README.md); the retry
contract of every change is in [workflows.md](workflows.md#changes-and-their-retry-contract).

## Profiles

| Profile | Tools |
| --- | --- |
| `projects-read` | `n8n.projects.list` |
| `projects-manage` | `n8n.projects.list`, `n8n.projects.create`, `n8n.projects.update` |
| `members-read` | `n8n.project-members.list` |
| `members-manage` | `n8n.project-members.list`, `n8n.project-members.add`, `n8n.project-members.set-role` |

These are in neither the workflow profiles nor each other, and the member profiles are recommended in none.
`n8n.projects.delete` and `n8n.project-members.remove` are in no profile and offered only by a `tools` list.

## Projects

The tools call only the project routes of the Public API; folders are not offered. Members are covered
below. The routes and their scopes (`project:list`, `project:create`, `project:update`, `project:delete`) are
those of n8n-io/n8n at commit `191a22e`.

**Allow-list.** The project allow-list (`project/PROJECT_ID`) applies as follows:

- `projects.list` keeps only the projects of the allow-list; n8n itself answers with every project of the
  instance. With an empty project allow-list it lists all of them, the same reading the workflow and execution
  tools give an empty list; this is the only project tool for which an empty list admits everything. As
  with `workflows.list`, a page can be empty after filtering while `has_more` is true. Personal projects are included when no allow-list is set; their names can identify a user.
- `projects.update` and `projects.delete` accept only a `project_id` of the allow-list, and therefore need a
  configured project allow-list: without a `project/PROJECT_ID` target no project is on the list, and both
  are refused locally. A foreign ID is refused the same way. Both refusals are invalid requests, made before a
  secret is read and before any request is sent, and neither names a project.
- `projects.create` is refused on a connection with a project allow-list, since the new project cannot
  already be on it. The request carries only `name`; n8n's `id`, `icon`, `description`, and telemetry tags
  are not offered, so a caller cannot choose the ID of the new project.
- A connection with a **workflow** allow-list refuses `create`, `update`, and `delete` of projects: they reach
  workflows beyond that list. `list` still works.

`projects.update` sends only `name`, the one field `updateProject` accepts. The Public API has no endpoint to
read a single project, so the change is not re-read.

**What deleting a project does.** `DELETE /projects/{id}` runs `ProjectsPublicController.deleteProject`
(`packages/cli/src/public-api/v1/controllers/projects.public.controller.ts`), which calls
`ProjectService.deleteProject(req.user, projectId)` (`packages/cli/src/services/project.service.ee.ts`)
without a migration target, and its query schema `DeleteProjectQueryPublicDto` is the empty strict object, so
the Public API accepts no transfer parameter (the editor's internal endpoint has `transferId`; the public one
has none). Without a target, n8n:

1. refuses anything but a team project (403 for a personal project),
2. deletes every workflow the project owns (`workflowService.delete(user, id, true)`),
3. deletes every credential the project owns,
4. deletes the project's data tables (when the data-table module is active),
5. removes its external-secrets connections and agent data (when those modules are active),
6. removes the project; workflows and credentials only shared into it lose that share.

Nothing is moved to another project and the deletion cannot be undone. The steps run one after another, not
in a transaction: a rejection part-way, for example a 409 because an owned workflow is still published, can
leave the project partly emptied. Qatlas therefore reports every provider rejection of a delete other than
403 and 404, as well as every unclear transport result, as "this change may have taken effect, read the
current state before repeating it". Because the Public API offers no transfer, the contract chosen here is
the only one it allows: a delete always deletes, and there is no argument to move content first.

**License and role.** All project routes carry `@Licensed(PROJECT_ROLE_ADMIN)`; `create` also needs the
global `project:create` scope. n8n answers 403 for a missing license (the license middleware), a missing API
key scope, and a role that may not manage projects, and this provider does not read the response body, so
every 403 of a project tool is reported as class `permission` with a message that names all three possible
causes (license, API key scope, role).

## Project members

The member tools call only the member routes of the Public API, as of n8n-io/n8n at the same commit
(scopes `user:list` and `project:manageMembers`).

- **Binding.** Like `projects.update`, every member tool, `list` included, accepts only a `project_id` of the
  project allow-list. Without a `project/PROJECT_ID` target, with a project outside it, with a workflow
  allow-list, or with a malformed `project_id` or `user_id`, the connection refuses locally, before a secret
  is read and before any request is sent, without naming the project.
- **Add.** `add` gives an existing user a role through a single relation: nobody is created or invited, and
  there is no email argument. It is not idempotent.
- **Roles.** `role` is one of `project:admin`, `project:editor`, `project:viewer`. `project:personalOwner`
  and instance roles are refused locally.
- **Output.** `list` returns no personal data beyond `id`, `email`, name, and `role`, each at most 256
  characters, with the data sensitivity `n8n-project-members` (personal data). Entries beyond `limit`
  (default 100) are cut and flagged `truncated`.
- **403.** n8n answers 403 for a missing Projects license, a missing API key scope, and a role that may not
  manage members, and the body is not read, so every 403 is the neutral message "license or role missing",
  class `permission`.
