---
description: >
  Describes the GitHub security tools: the code scanning, Dependabot, and secret scanning alerts of a repository, the global, repository, and organization security advisories, and code quality findings, with their targets and cuts.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-23
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

The members an alert of any kind carries beyond these are cleaned the same way, and each of their cuts says so
in its own `*_truncated` member, never in `truncated`.

A secret scanning alert never shows the found secret, and its comments are withheld because they may repeat it.
The request asks GitHub to hide the secret (`hide_secret=true`), and the answer is decoded into a shape that
has no field for the secret, a snippet, or a comment, not even inside the first location, so nothing else
GitHub sends can reach a result, an error, or a log. An answer that cannot be read or is too large fails with
a fixed message that quotes nothing of it.

The token rights are listed under [Security](setup.md#security).
Sources: https://docs.github.com/en/rest/code-scanning/code-scanning,
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

`github.global-advisories.list` reads the GitHub Advisory Database, GitHub-wide, and gives each entry every
member `.get` does; GitHub answers the type `reviewed` when `type` is omitted, and `is_withdrawn` false
adds no filter. `github.code-quality-findings.get` reads one finding by `finding_number`; GitHub also lists
findings, and that list is not offered.

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
description and a rule help at 4000, and the description of a repository advisory in a list at
2000, and the Markdown of a finding message at 2000; vulnerabilities are capped at 20 per advisory, functions
at 10 per vulnerability, references at 20, and `truncated` says so. The lists an advisory carries beyond these
(identifiers, credits, collaborators) are capped at 100 and say so in their own `*_truncated` member, never in
`truncated`. Draft and triage advisories hold non-public vulnerability details; they reach only a token whose
account GitHub allows to see them.

An account inside an advisory carries the fields GitHub's own REST answers give it; the full profile is read
with the account tools. The temporary private fork of a repository advisory is a repository: one outside the
connection's targets keeps only its identifiers (`id`, `node_id`, `full_name`), and the clone token GitHub
sends with it is never shown.

The token rights are listed under [Security](setup.md#security). Sources:
https://docs.github.com/en/rest/security-advisories/global-advisories,
https://docs.github.com/en/rest/security-advisories/repository-advisories, and
https://docs.github.com/en/rest/code-quality/code-quality.
