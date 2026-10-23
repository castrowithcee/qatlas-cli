---
description: >
  Describes what is specific to the Forgejo provider: product detection and the supported versions, token scopes and
  repository-bound tokens, the tools and arguments only Forgejo has, and the render forms. Everything else is
  described once in the Gitea provider documentation.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-23
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
- The user object carries `pronouns`; the repository object carries `wiki_branch` and `has_wiki_contents`.
- `settings.get` with `repository` also carries `forks_disabled`.
- `signing-keys.get` reads the SSH public key with `format` `ssh`; `pub` is Gitea's form and refused.
- The render modes are `markdown`, `gfm`, and `comment`, plus `file` for `markup.render`. The repository reference
  is the address of the repository on the instance, `https://HOST/OWNER/REPO`. Both tools take `wiki`, and
  `markup.render` takes `branch_path`; neither is offered by Gitea.
- Issue lists and searches take `sort`, and `issues.search` takes `priority_repo_id`. `issues.update` takes
  `updated_at`, the time read before, to refuse an update after the issue changed; there is no `content_version`,
  `projects`, or `time_estimate`.
- Pull requests: `pull-requests.list` takes `base` and `head` (from Forgejo 17) and the pull request carries `flow`;
  Gitea has `base_branch` and `content_version` instead. See [Pull requests](gitea/pull-requests.md).
- A release asset may be a link to an external address (`type` `external`). Qatlas never creates one (`external_url`
  is not offered), never changes its address, and `release-assets.download` refuses it. See
  [Releases](gitea/releases.md).
- Attachments of issues and comments carry `type` and allow `browser_download_url` in the edit; the latter is not
  offered, `*-attachments.update` changes only `name`, and an external attachment is refused by
  `*-attachments.download`. See [Attachments](gitea/attachments.md).

## Not on Forgejo

Forgejo has no project API, so the project board tools (`projects.*`, `project-columns.*`, `project-issues.*`) are
not registered. See [Projects](gitea/projects.md).
