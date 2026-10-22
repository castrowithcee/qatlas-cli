---
description: >
  Describes the GitHub security tools: the code scanning, Dependabot, and secret scanning alerts of a repository, the global, repository, and organization security advisories, and code quality findings, with their targets, cuts, and tokens.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub security

The security tools of the [GitHub provider](README.md). All of them only read. The not-recommended setup
profile `security` selects the six alert tools and the five advisory and finding tools, eleven in all; the
recommended profile `read` is unchanged.

## Security alerts

Six tools read the alerts of one repository; they change nothing, so an alert is neither closed, dismissed,
nor assigned.

| Tool | Does |
| --- | --- |
| `github.code-scanning-alerts.list` | lists code scanning alerts |
| `github.code-scanning-alerts.get` | reads one code scanning alert with the rule help |
| `github.dependabot-alerts.list` | lists Dependabot alerts |
| `github.dependabot-alerts.get` | reads one Dependabot alert with the advisory description |
| `github.secret-scanning-alerts.list` | lists secret scanning alerts without the secret |
| `github.secret-scanning-alerts.get` | reads one secret scanning alert without the secret |

Each list answers one bounded batch, newest first, with `limit`, `next_cursor`, and `has_more`; a cursor
belongs to the repository and the filters of its first batch. Each `.get` takes an `alert_number` as the list
reports it. Every tool takes the optional `repository` argument and is checked against the connection's
targets before a credential is resolved; an organization-wide alert list is not offered.

Alert texts (rule and advisory descriptions, messages, dismissal comments, paths) come from scanners and other
accounts and are untrusted data. Each is cut at 500 characters (4000 for the rule help and the advisory
description of a `.get`), and `truncated` says so.

The members a code scanning alert carries beyond these are cleaned the same way, and each of their cuts says
so in its own `*_truncated` member, never in `truncated`.

A secret scanning alert never shows the found secret. The request asks GitHub to hide it (`hide_secret=true`),
and the answer is decoded into a shape that has no field for a secret value, a snippet, or a comment, so
nothing else GitHub sends can reach a result, an error, or a log. An answer that cannot be read or is too
large fails with a fixed message that quotes nothing of it. An alert reports its type, state, resolution,
validity, times, and the first location (kind, path, lines) only.

| Tool | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.code-scanning-alerts.*` | `security_events` or `repo`; `public_repo` for a public repository | Code scanning alerts: read |
| `github.dependabot-alerts.*` | `security_events` or `repo`; `public_repo` for a public repository | Dependabot alerts: read |
| `github.secret-scanning-alerts.*` | `repo` or `security_events` | Secret scanning alerts: read |

GitHub answers `not-found` for a repository without the feature enabled or not visible to the token, and code
scanning answers 403 without GitHub Advanced Security. Sources:
https://docs.github.com/en/rest/code-scanning/code-scanning,
https://docs.github.com/en/rest/dependabot/alerts,
https://docs.github.com/en/rest/secret-scanning/secret-scanning, and
https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens.

### Advisories and code quality findings

Five more tools read advisories and code quality findings; they change nothing, so an advisory is neither
created, edited, published, nor closed.

| Tool | Does |
| --- | --- |
| `github.global-advisories.list` | lists global security advisories |
| `github.global-advisories.get` | reads one global security advisory |
| `github.repository-advisories.list` | lists the security advisories of a repository |
| `github.organization-advisories.list` | lists the security advisories of an organization's repositories |
| `github.code-quality-findings.get` | reads one code quality finding |

`github.global-advisories.list` reads the GitHub Advisory Database, GitHub-wide; GitHub answers the type
`reviewed` when `type` is omitted, and `is_withdrawn` false adds no filter. `github.code-quality-findings.get`
reads one finding by `finding_number`; GitHub also lists findings, and that list is not offered.

The global tools name no repository: a connection without targets, or one whose targets name only owners, may
use them, and a connection whose targets name a repository or a project is refused as `invalid-request`
before a credential is resolved. The organization list takes the optional `owner` argument as `orgs/LOGIN`,
which must be an organization the connection's targets name as an owner (or any organization on a connection
without targets); a user owner and a repository or project target alone are refused the same way. The
repository list and the finding read take the optional `repository` argument like the alert tools.

The lists page by the `after` cursor GitHub announces in its `Link` header; `next_cursor` carries it together
with the batch size of the first batch and belongs to the tool, the target, and the filters of that batch.

Advisory summaries, descriptions, references, package names, and finding messages come from other accounts and
are untrusted data. Summaries, rule descriptions, and messages are cut at 500 characters, a global advisory
description of a `.get` and a rule help at 4000, and the description of a repository advisory in a list at
2000; vulnerabilities are capped at 20 per advisory, functions at 10 per vulnerability, references at 20, and
`truncated` says so. Draft and triage advisories hold non-public vulnerability details; they reach only a
token whose account GitHub allows to see them.

| Tool | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.global-advisories.*` | none | none |
| `github.repository-advisories.list` | `repo` or `repository_advisories:read` for a private repository or an unpublished advisory | Repository security advisories: read |
| `github.organization-advisories.list` | `repo` or `repository_advisories:write`, and an organization owner or security manager | Repository security advisories: write |
| `github.code-quality-findings.get` | `repo`; `public_repo` for a public repository | Code quality: read |

The organization list needs the write-level permission although it only reads; that is how GitHub documents
it. GitHub answers the finding read with 403 when code quality is not enabled or the token may not read it,
and the repository list with `not-found` for a repository the token cannot see. Sources:
https://docs.github.com/en/rest/security-advisories/global-advisories,
https://docs.github.com/en/rest/security-advisories/repository-advisories, and
https://docs.github.com/en/rest/code-quality/code-quality.
