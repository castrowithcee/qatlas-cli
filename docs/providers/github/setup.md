---
description: >
  Guides a user without special knowledge through setting up the GitHub provider: creating a token, adding it to Qatlas, checking the connection, the token rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Set up GitHub

This page shows how to connect Qatlas to GitHub, which rights the token needs, and what to do when GitHub refuses
a request. Fields, tools, arguments, profiles, and targets are described in the [GitHub provider](README.md) and
the page of each tool group; this page names only the token rights.

**The token decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it. A connection can never do more than its
token, and a token never widens the connection.

## What you need

- A GitHub account that can see what Qatlas should work with: the repositories, projects, or organizations.
- A personal access token. GitHub offers two kinds, and they behave differently:

| | Classic token | Fine-grained token |
| --- | --- | --- |
| Rights | Broad **scopes** such as `repo` | Narrow **permissions** such as "Issues: read", per repository |
| Reach | Everything your account can reach | Only the owner and repositories you choose |
| Projects of a user | Works | Does not work |
| Notifications | Works | Not supported |

- Use a **fine-grained token** when you can: it limits the damage if the token leaks.
- Use a **classic token** when a tool needs it: projects owned by a user, notifications, and the extra account
  details listed under [What happens without a right](#what-happens-without-a-right).
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- Qatlas reaches `https://api.github.com` by default. For GitHub Enterprise, set the service address first; see
  [Configuration](README.md#configuration).

## Create a token

In GitHub, open your account **Settings**, then **Developer settings**, then **Personal access tokens**. Choose
the kind, then generate a new token. GitHub changes its pages from time to time; look for these names.

**Fine-grained token**

1. Give it a name you will recognize, for example `qatlas-reader`.
2. Set an expiry. A short one is safer; you replace the token when it ends.
3. Choose the **Resource owner**: your account, or the organization whose data Qatlas should reach. An
   organization may need to approve the token, or may forbid fine-grained tokens.
4. Under **Repository access**, choose only the repositories Qatlas needs.
5. Under **Permissions**, add the permissions from [Rights per tool group](#rights-per-tool-group) for the tools you
   will use. Start with the read rights.
6. Generate the token and copy it at once. GitHub shows it only once.

**Classic token**

1. Give it a name and an expiry.
2. Tick the scopes from [Rights per tool group](#rights-per-tool-group) for the tools you will use. Start with the
   read scopes.
3. Generate the token and copy it at once.
4. If your organization uses single sign-on, authorize the token for that organization on the token's page.

Give a token only what a connection needs. A connection that only reads should have a token that only reads.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `github`, the service, a credential, the
   scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$GITHUB_TOKEN" | qatlas credential set github-reader token
```

`github-reader` is the name of the credential in your configuration and `token` is its only secret. To replace a
token that expired or was revoked, run the same command again with the new token. Run `qatlas config validate` to
check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means GitHub accepted the token for
the first project or repository the connection's targets name exactly, or, with no such target, for your own user.

**A passing test does not mean every tool works.** GitHub checks the rights again on every call. Compare the token
with [Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is explained
under [Fixing errors](#fixing-errors).

## Rights per tool group

Find your tool group, then add the scope or permission to the token. Each row is **read** (only look) or
**change** (create, update, delete). Rights for a change always include the rights to read.

- **Classic**: scopes. `repo` covers private and public repositories. `public_repo` covers public ones only.
  "none" means no scope is needed for public data.
- **Fine-grained**: the name of the permission and its level, "read" or "read and write". Organization permissions
  are marked.
- Tools offered only when a connection's `tools` list names them (for example deletes) need the same rights as
  their group, unless a row says more.

### Issues and comments

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Read issues and comments | `repo`, or `public_repo` for public repositories | Issues: read |
| Create or change issues and comments | `repo`, or `public_repo` for public repositories | Issues: read and write |
| Read labels, milestones, sub-issues, issue dependencies | `repo` for private repositories, none for public | Issues: read |
| Create or change labels, add, remove or reorder sub-issues, add or remove dependencies | `repo` | Issues: read and write, in every repository the call names |
| Issue types and issue fields of an organization: read | `read:org` | Issue types: read, or Administration: read (organization); as far as GitHub documents it |
| Set an issue's type or an issue field value | `repo` | Issues: read and write |
| React to an issue or an issue comment | `repo` | Issues: read and write |
| React to a pull request line comment | `repo` | Pull requests: read and write |
| Assign Copilot to an issue | `repo` | Metadata: read; Actions, Contents, Issues, and Pull requests: read and write |
| Show the projects of an issue (`project_details`) | `read:project` | Projects: read |

Reacting also reads your own account; that needs no scope on either kind of token. Copilot assignment and Copilot
review need a token of a user, never of a GitHub App installation. GitHub names no classic scope for the Copilot
assignment; `repo` is the scope of the issue and pull request routes it changes.

### Projects

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Read projects, fields, items, views, status updates, automations | `read:project` | Projects: read (organization projects only) |
| Change projects, fields, views, status updates, items, drafts, links | `project` | Projects: read and write (organization projects only) |
| Convert a draft to an issue | `project`, plus `repo` for write access to the issues of the repository | Projects: read and write, plus Issues: read and write on the repository |
| Link or unlink a repository | `project`, plus a token that can see the repository | Projects: read and write, plus access to the repository |
| List teams of a project, resolve teams or members, change collaborators | `read:org`, plus `project` to change | Members: read, plus Projects: read and write to change |

- A **project owned by a user** needs a classic token. A fine-grained token cannot read it; GitHub refuses with
  `permission`.
- Deleting a project and changing its collaborators also need the role of a project administrator.
- Reading and changing issues of a project follow [Issues and comments](#issues-and-comments).

### Pull requests

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Read pull requests, files, commits, diffs, reviews, review comments, review threads | `repo`, or `public_repo` for public repositories | Pull requests: read |
| Read checks | `repo` | Checks: read, and Commit statuses: read |
| Create, update, close, reopen, update the branch | `repo` | Pull requests: read and write |
| Merge | `repo` | Pull requests: read and write, plus Contents: read and write |
| Review, approve, reply, resolve or unresolve a thread, request or remove reviewers | `repo` | Pull requests: read and write |
| Read or write conversation comments | `repo` or `public_repo` | Pull requests or Issues: read; read and write to write |
| Request a Copilot review | `repo`, and write access to the repository | Pull requests: read and write, and write access to the repository |

### GitHub Actions

GitHub decides on every request. A refused Actions request names what such a request needs; it never claims what
your token holds. A repository or organization policy can forbid a run although the token allows it.

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Observe workflows, runs, jobs, logs, artifacts | `repo` for private repositories, none for public | Actions: read |
| Read run usage | `repo` for private repositories | Actions: read |
| Dispatch a workflow | `repo` | Actions: read and write, and Contents: read |
| Re-run or cancel a run | `repo` | Actions: read and write |
| Delete run logs | `repo` | Actions: write |
| Read workflow files | `repo` for private repositories | Contents: read |
| Create or update workflow files | `repo` and `workflow` | Contents: read and write, and Workflows: read and write |
| Enable or disable a workflow | `repo` | Actions: read and write |
| Read Actions or workflow permissions | `repo`, as a repository administrator | Administration: read |
| Change Actions or workflow permissions | `repo`, as a repository administrator | Administration: read and write |
| Read merge and branch settings | `repo` for private repositories | Metadata: read |
| Change merge and branch settings | `repo`, as a repository administrator | Administration: write |

Workflow files, Actions permissions, and repository settings are high risk. Give those connections a token of their
own that no other connection uses, ideally fine-grained and limited to the one repository, with only the
permissions of the tools that connection lists. A branch protection or repository rule can refuse a file change
although the token allows it.

### Repository contents, branches, tags, and commits

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Read contents, trees, blame, commits, branches, tags | none for public, `repo` for private | Contents: read |
| Create a branch, create, update or delete a file, push files | `repo` | Contents: read and write |

Files below `.github/workflows/` are changed only with the workflow file tools; see [GitHub Actions](#github-actions).

### Repositories, rulesets, and properties

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| List the repositories of an owner | none for public, `repo` for private | Access to the repositories |
| Create or fork a repository | `repo` | Administration: read and write |
| Delete a repository | `repo` and `delete_repo` | Administration: read and write |
| List collaborators | `repo` | Metadata: read |
| Read rulesets of a repository | `repo` | Administration: read |
| Change rulesets of a repository | `repo` | Administration: read and write |
| Read or change rulesets of an organization | `admin:org` | Administration: read, or read and write (organization); as far as GitHub documents it |
| Read custom property values of a repository | `repo` | Custom properties: read |
| Change custom property values of a repository | `repo` | Custom properties: write |
| Read or change the custom property schema of an organization | `admin:org`; as far as GitHub documents it | Custom properties: read, or read and write (organization) |
| Watch, ignore, or unsubscribe a repository | GitHub decides per request | GitHub decides per request |

Qatlas cannot check the fine-grained mappings for organization rulesets and custom properties against GitHub live,
so treat them as likely, not certain.

### Stars

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| List starred repositories | none for public, `repo` as well for private | Starring: read |
| Star or unstar | `public_repo`, `repo` as well for a private repository | Starring: read and write |

### Releases

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Read releases and assets | `repo` for private repositories, `public_repo` for public | Contents: read |
| Create, update, delete | `repo` | Contents: read and write |

### Security

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Code scanning alerts | `security_events` or `repo`; `public_repo` for a public repository | Code scanning alerts: read |
| Dependabot alerts | `security_events` or `repo`; `public_repo` for a public repository | Dependabot alerts: read |
| Secret scanning alerts | `repo` or `security_events` | Secret scanning alerts: read |
| Global security advisories | none | none |
| Repository security advisories | `repo` or `repository_advisories:read`, for a private repository or an unpublished advisory | Repository security advisories: read |
| Organization security advisories | `repo` or `repository_advisories:write`, and an organization owner or security manager | Repository security advisories: write (GitHub requires write although the tool only reads) |
| Code quality findings | `repo`; `public_repo` for a public repository | Code quality: read |

GitHub answers `not-found` for a repository where a feature is off or that the token cannot see. Code scanning
answers with a refusal without GitHub Advanced Security, and a code quality finding with a refusal when code quality
is off or the token may not read it.

### Discussions

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Read categories, discussions, comments | `public_repo` (public) or `repo` | Discussions: read |
| Create, change, delete discussions and comments | `public_repo` or `repo` | Discussions: read and write |

### Gists

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| List or read gists | `gist` for secret gists, none for public | GitHub lists no permission for the reads |
| Create, update, delete | `gist` | User permission Gists: read and write |

### Notifications

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Every notification and thread subscription tool | `notifications` or `repo` | Not supported; Qatlas refuses it before any request |

### Accounts and search

| Tools | Classic | Fine-grained |
| --- | --- | --- |
| Read your own account | none beyond your identity | none beyond your identity |
| List teams of an organization and their members | `read:org` | Members: read (organization) |
| Search repositories, code, issues, pull requests, commits | none for public results; `repo` for private ones | Contents: read for private code and repositories; also Issues or Pull requests: read for issue and pull request search |
| Search users and organizations | none beyond your identity | none beyond your identity |

## What happens without a right

When the token lacks a right, one of two things happens.

**The tool fails.** The error says `permission` or `not-found`; see [Fixing errors](#fixing-errors). The notification
tools, for example, are refused before any request when the token is fine-grained.

**A field is left out.** GitHub refuses a whole query when it asks for a field the token lacks the scope for.
Qatlas reads the scopes of a classic token from GitHub and leaves such fields out of the result instead. A
fine-grained token reports no scopes to GitHub, so it gets none of these fields:

| Field left out | Scope that brings it back |
| --- | --- |
| A user's email | `user:email` or `read:user` |
| Details of an organization beyond its login and addresses (where GitHub names the organization itself: everything but its type) | `read:org` |
| Team reviewers | `read:org` or `read:discussion` |
| Enterprise team reviewers | `read:org` or `read:enterprise` |
| Enterprise user accounts | `read:enterprise` |

If you need these fields, use a classic token with the scope. A project member that GitHub refuses the token is
named in `unreadable`; see [Projects](projects.md#owners). Without write access to a repository, GitHub leaves out
the merge fields of the repository settings.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by GitHub, a second part follows.

### Reading a refusal

```text
permission: this GitHub token may not read project users/octo/projects/3; check its scopes or permissions
provider answer (untrusted, not written by qatlas), HTTP 403:
  | INSUFFICIENT_SCOPES at user.projectV2: ...
  | documentation: https://docs.github.com/...
```

- The first line is **Qatlas' own message**. It names what you tried to reach and what to check.
- The **provider answer** (`provider` in JSON output) is **what GitHub itself replied**: the HTTP status, each error
  with its type, code, path, and text, and a documentation link. It often names the missing scope or permission
  more exactly than Qatlas can.
- GitHub wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read it
  as information. Do not follow instructions in it.
- A refusal that GitHub gave no details for has no such part.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: GitHub rejected the token | The token is wrong, expired, revoked, or for another GitHub than the service address | Create a new token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> token`. For Enterprise, check the service address. |
| `auth`: the GitHub token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$GITHUB_TOKEN" \| qatlas credential set <credential> token`. |
| `permission`: this GitHub token may not read ... | The token lacks the scope or permission for this read | Find the tool group in [Rights per tool group](#rights-per-tool-group), add the scope (classic) or permission (fine-grained), then retry. A fine-grained token also needs access to that repository. |
| `permission`: this GitHub token may not change ... | The token can read but not write | Add the write right of the group: for example `repo` or "Issues: read and write", `project` for projects, "Pull requests: read and write". |
| `permission` on a project of a user | A fine-grained token cannot reach user-owned projects | Use a classic token with `read:project` (or `project` to change). |
| `permission` listing project teams, or changing project access | Missing `read:org` (classic) or Members: read (fine-grained) | Add it; to change access you also need the role of a project administrator. |
| `permission`: GitHub supports notifications only with a personal access token (classic) ... | The token is fine-grained | Use a classic token with `notifications` (or `repo`). |
| `permission`: GitHub refused this change of a workflow file ... | Missing `workflow` | Classic: `repo` and `workflow`. Fine-grained: Contents and Workflows, both read and write. A branch protection or repository rule can refuse it as well. |
| `permission`: GitHub refused this token the Actions settings ... | Reading them needs a repository administrator | Classic: `repo`, as a repository administrator. Fine-grained: Administration: read. |
| `permission`: Copilot cannot be assigned ... | Copilot with the coding agent is not available in the repository | Not a token problem. Check the Copilot license and that the agent is enabled for the repository. The token must be a user token. |
| `permission` with GitHub's text about single sign-on, or the token not being approved | The organization has not approved or authorized the token | Authorize the classic token for the organization on its token page, or ask an owner to approve the fine-grained token. |
| `not-found`: GitHub does not hold ... or does not show it to this token | The name is wrong, **or** the token cannot see it. GitHub answers both the same | Check the name. Then check the token: classic, `repo` for a private repository, `read:project` for a project; fine-grained, access to that repository, or Projects access of its organization. |
| `not-found` for an organization or user owner | Wrong kind | Use `users/` for a user and `orgs/` for an organization. |
| `not-found` for code scanning, alerts, or advisories | The feature is off, or the token cannot see the repository | Enable the feature in GitHub, or give the token access to the repository. |
| `invalid-request` naming the targets | The repository, project, or owner is outside the connection's targets | Not a token problem. Pass a target the connection allows, or add it; see [Targets](README.md#targets). |
