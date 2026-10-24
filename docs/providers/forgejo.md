---
description: >
  Describes what is specific to the Forgejo provider: product detection and the supported versions, token scopes and
  repository-bound tokens, the tools and arguments only Forgejo has, and the render forms. Everything else is
  described once in the Gitea provider documentation.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-24
---

# Forgejo

This provider reads the account behind an access token, the repositories, and the instance of a self-hosted Forgejo
instance, and reads and changes its issues. It shares its code and its rules with [Gitea](gitea/README.md), which
describes once what both have: the configuration, the targets, the tool groups, the version check and connection
test, pagination, errors, bounds, the excluded operations, the setup profile, and the note on public instances. This
page lists only what differs.

## Configuration

The service uses `provider: forgejo` and the same `base_url` rules as Gitea. The profile and the tool IDs carry the
prefix `forgejo.`.

## Versions

Forgejo 15 (LTS) and later. The connection test recognizes Forgejo by `GET /api/forgejo/v1/version`; an instance
that blocks that path by proxy is taken for Gitea and refused. The version below `/api/v1` carries the suffix
`+gitea-...`, the compatibility marker Forgejo reports, which `version.get` returns unchanged. Development builds
count as their number.

## Tokens

Forgejo requires at least one scope on every token, also for reading public repositories; the scopes per tool group
are in the table of the Gitea page. Forgejo 15 and later also issues tokens bound to single repositories, limited to
the `repository` and `issue` categories, and tokens restricted to public resources. Both answer HTTP 404 instead of
403 for what they do not reach; Qatlas reports it as `not-found` like any refusal. A repository-bound token is an
additional depth of defense: the targets of the connection stay the boundary.

## Only on Forgejo

- `nodeinfo.get` reads the NodeInfo document of the instance. Its `metadata` member is free-form and bounded in
  depth, length, and key count.
- `repositories.list` takes `order_by` for the token's own repositories (not together with `owner`).
- The organization object carries `created`. See [Organizations](gitea/organizations.md).
- Organization blocking uses `list_blocked`, `block`, and `unblock`, lists only block entries, has no
  `org-blocks.check`, and takes no `note`. See [Organizations](gitea/organizations.md).
- The team object and the team settings carry no `visibility`; every team route by ID requires membership of the team
  or site administration, not only the organization's ownership. See [Teams](gitea/teams.md).
- The user object carries `pronouns` (Gitea 28 and later carries `type` instead), and `users.search` takes `sort`. See
  [Users](gitea/users.md). The repository object carries `wiki_branch` and `has_wiki_contents`.
- `settings.get` with `repository` also carries `forks_disabled`.
- Blocking an account user uses `list_blocked`, `block`, and `unblock`, lists only block entries, has no
  `user-blocks.check`, and takes no `note`; `user-settings` also carries `pronouns`, `hide_pronouns`, and
  `enable_repo_unit_hints`. See [Users](gitea/users.md).
- Quota: the `quota.*` and `admin-quota-*` tools exist on Forgejo only (Gitea has no quota). See [Quota](#quota).
- `signing-keys.get` reads the SSH public key with `format` `ssh`; `pub` is Gitea's form and refused.
- `repository-signing-keys.get` serves only `gpg`.
- The render modes are `markdown`, `gfm`, and `comment`, plus `file` for `markup.render`. The repository reference
  is the address of the repository on the instance, `https://HOST/OWNER/REPO`. Both tools take `wiki`, and
  `markup.render` takes `branch_path`; neither is offered by Gitea.
- Issue lists and searches take `sort`, and `issues.search` takes `priority_repo_id`. `issues.update` takes
  `updated_at`, the time read before, to refuse an update after the issue changed; there is no `content_version`,
  `projects`, or `time_estimate`.
- Pull requests: `pull-requests.list` takes `base` and `head` (from Forgejo 17) and the pull request carries `flow`;
  Gitea has `base_branch` and `content_version` instead. See [Pull requests](gitea/pull-requests.md).
- Review comments: `pull-request-review-comments.get`, `.create`, and `.delete` (listed-only) exist on Forgejo only;
  `reply`, `resolve`, and `unresolve` are Gitea's. `extra_lines_count` needs Forgejo 16. See
  [Pull request reviews](gitea/pull-request-reviews.md).
- A release asset may be a link to an external address (`type` `external`). Qatlas never creates one (`external_url`
  is not offered), never changes its address, and `release-assets.download` refuses it. See
  [Releases](gitea/releases.md).
- Commits and Git objects: `git-blobs.list`, `git-notes.set`, and `git-notes.delete` exist only on Forgejo;
  `commits.list` has no `since` and `until`, a commit status has no `skipped`, the comparison switches need Forgejo 17,
  and comparison and annotated tag objects carry Forgejo members. See [Commits](gitea/commits.md).
- Attachments of issues and comments carry `type` and allow `browser_download_url` in the edit; the latter is not
  offered, `*-attachments.update` changes only `name`, and an external attachment is refused by
  `*-attachments.download`. See [Attachments](gitea/attachments.md).
- Branches: there is no `branches.update-ref` and `branches.list` has no `q` filter; Gitea has both. See
  [Branches](gitea/branches.md).
- Repository lifecycle: `repositories.get-sync-state` and `repositories.convert` (listed-only) exist on Forgejo only.
  `repositories.sync-fork` calls `sync_fork` where Gitea calls `merge-upstream` and has no `ff_only`.
  `repositories.update` takes `wiki_branch` and `globally_editable_wiki`, which Gitea does not, and offers the merge
  styles `manually-merged` and `rebase-update-only`; Gitea's `projects_mode`, `has_code`, `allow_manual_merge`,
  `autodetect_manual_merge`, and `allow_merge_update` are refused. See [Repository lifecycle](gitea/repositories.md).
- Contents: `contents.get-ext` and `contents.get-many` do not exist, `archives.get` has no `paths`, and entries carry
  `last_commit_when` instead of Gitea's commit dates, message, LFS, and mode members. `contents.change-files` has only
  the operations `create` and `update`, and the new-branch overwrite is `force_overwrite_new_branch` instead of
  `force_push`; neither is offered. See [Contents](gitea/contents.md).
- Accesses: webhooks carry `content_type` and `url` instead of Gitea's `name`; push mirrors carry `branch_filter`.
  `access-tokens.list` exists on Forgejo only; `access-tokens.get-current` and `access-tokens.delete-current` do not.
  See [Accesses](gitea/accesses.md).

- `issue-locks.*` exist from Forgejo 17 and send the lock reason as `reason`; Gitea names it `lock_reason`. Older
  versions report the tools as unavailable. See [Issue locks](gitea/issue-locks.md).

## Quota

| Tool | Effect | Reads |
| --- | --- | --- |
| `quota.get` | read | groups, rules, and storage in use |
| `quota.list-artifacts`, `quota.list-attachments`, `quota.list-packages` | read | what counts towards the quota |
| `quota.check` | read | whether an action on one `subject` is within the quota |

All five read the quota of the account behind the token, or with `owner` (`orgs/NAME`) that of an organization. The
scopes presumably are `read:user` for the account and `read:organization` for an organization, which the instance
restricts further to the organization's owners; they are not confirmed against a live instance.

- An organization must be named by an `orgs/NAME` target when the connection has targets; no `users/NAME` target is
  needed. It is refused before the token is read and without naming it. The account needs, with targets, a
  `users/NAME` target for it and no repository target, as for `user-settings.get`; it is then proven by reading
  `GET /user` first.
- In the lists, a row that names a repository outside the targets is dropped, and with targets so is a row that names
  none; packages belong to their owner and are not filtered. A page may therefore hold fewer rows than `limit` and
  still have a successor.
- Names, versions, and addresses are untrusted data.
- An instance can switch the quota off; it then does not serve the routes and answers 404, like it does for an
  organization or a token it does not reach. After a 404 the tool reads `GET /user` or `GET /orgs/NAME`: if that
  answers, the quota is reported as not available (`provider-error`), otherwise as `not-found`. A 403 is `not-found`.

### Quota administration

| Tool | Effect | Acts on |
| --- | --- | --- |
| `admin-quota-groups.list`, `admin-quota-groups.get` | read | quota groups with their rules |
| `admin-quota-groups.create` | create | an empty group |
| `admin-quota-groups.delete` | delete | a group |
| `admin-quota-groups.add-rule`, `admin-quota-groups.remove-rule` | update | the rules of a group |
| `admin-quota-group-users.list` | read | the users of a group |
| `admin-quota-group-users.add`, `admin-quota-group-users.remove` | update | the users of a group |
| `admin-quota-rules.list`, `admin-quota-rules.get` | read | quota rules |
| `admin-quota-rules.create`, `admin-quota-rules.update` | create, update | a rule's `limit` and `subjects` |
| `admin-quota-rules.delete` | delete | a rule |
| `admin-user-quota.get` | read | groups, rules, and storage in use of one user |
| `admin-user-quota.set-groups` | update | replaces all groups of one user |

- They need site administration and the token scopes `read:admin` and `write:admin`; they reach every user, so only a
  connection without targets may call them, and a connection with targets is refused before the token is read. They
  are in no profile.
- Every change needs `confirm` and has its own data classification; `admin-quota-groups.delete` and
  `admin-quota-rules.delete` are listed-only: only a connection whose `tools` list names them offers them.
  A group is created empty; its rules are added with `add-rule`.
- The lists are not paged: they return at most 200 rows and set `has_more` when there are more.
- A switched-off quota is detected as described under [Quota](#quota), but from a 404 on these routes: the tools read
  the rule list to tell a missing group, rule, or user from missing routes, and then `GET /admin/users` with
  `limit=1`, which exists with or without quota.

## Not on Forgejo

Forgejo has no project API, so the project board tools (`projects.*`, `project-columns.*`, `project-issues.*`) are
not registered. See [Projects](gitea/projects.md).
- `repository-licenses.list` is not registered either; Forgejo has no endpoint for the licenses of a repository. See
  [Topics and templates](gitea/topics-templates.md).
- Actions: the API grew in steps, so most run tools need Forgejo 16 or 17; runs, tasks, and dispatch are available
  from 15, artifacts from 16. Runs, jobs, artifacts, and filters have Forgejo's own forms, there is no owner level,
  no attempts, no `force-cancel`, `rerun-failed`, or `approve`, no `workflows.list` or `workflows.get`, and
  `workflow-job-logs.get` takes `attempt`, `step`, `q`, `qi`, and `format`. See [Actions](gitea/actions.md).
- Packages: there is no `package-versions.list`, `package-versions.get-latest`, or `packages.delete`, `terraform` is
  not a package type, and the size of a package file is `Size` in the API and `size` in the result. See
  [Packages](gitea/packages.md).
- Actions administration: secrets and variables carry no `description`, and `actions-secrets.list` reads the secrets
  of a user only from Forgejo 17. Runners have Forgejo's own members and a paged list, there is no
  `actions-runners.update` and no `workflows.enable` or `workflows.disable`, and `actions-runner-jobs.search` is
  Forgejo's own. See [Actions administration](gitea/actions-admin.md).
- Instance administration: `admin-runners.update`, `admin-workflow-runs.list`, `admin-workflow-jobs.list`, and
  `admin-packages.list` are not registered; Forgejo lists runners with a paged list and `visible`, and has its own
  `admin-runner-jobs.search`. See [Instance administration](gitea/administration.md).
