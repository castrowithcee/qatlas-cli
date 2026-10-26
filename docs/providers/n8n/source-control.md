---
description: >
  Describes the n8n source control tools: the instance-wide gate, their profile, why pull and push are
  offered only by a tools list, the limits on their arguments, and how a conflict is returned.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# n8n source control

Configuration, targets, error classes, and cross-group limits are in [README.md](README.md); the retry
contract of every change is in [workflows.md](workflows.md#changes-and-their-retry-contract).

The tools never set up or change the connection to the repository, and do not touch environments or
promotions. They are instance-wide (refused locally on a connection with any `project/` or `workflow/` target,
before a secret is read). The rights are in [rights per tool group](setup.md#rights-per-tool-group).

Profile `sourcecontrol-read` holds `n8n.source-control.status`, in none of the other profiles.
`n8n.source-control.pull` and `n8n.source-control.push` are in no profile and offered only by a `tools` list.

- **Status.** `direction` is required (`push` or `pull`, no default). The result lists at most 200 affected
  objects. `file` is the path of the file in the instance's Git working folder; Qatlas gives it out as text
  and never reads the file. It changes nothing.
- **Pull** imports the connected Git branch into the instance and can overwrite workflows, credentials,
  variables, and other objects. It is effect `execute`, non-idempotent, and always needs confirmation.
  `force` (default `false`) discards local changes that would otherwise block the pull. `auto_publish` is
  `none` (default, nothing is published by the pull), `all` (publishes every imported workflow), or
  `published` (publishes those that were published locally before the import); both are always sent
  explicitly, and any other value is refused locally.
- **Push** commits and pushes objects to the repository, an external change. It has the same effect, risk,
  and tool-list rule as pull. `commit_message` is 1 to 1000 characters (UTF-16 units, as n8n counts them) and,
  narrower than n8n, contains no control character except a line feed. `files` holds 1 to 50 distinct
  objects; `id` is a plain n8n identifier and `type` is one of `credential`, `workflow`, `tags`,
  `variables`, `folders`, `project`, `datatable` (the generic `file` type n8n also lists is not accepted).
  `force` (default `false`) pushes even objects with unresolved conflicts.
- **Conflict (HTTP 409).** n8n refuses a pull because of local changes or merge conflicts, and a push because
  of unresolved conflicts, and changes nothing. This is a result, not an error: `outcome` is `conflict`,
  `conflict` is `true`, and `files` lists the entries n8n reports. Qatlas does not retry and does not set
  `force` by itself; the caller decides.
- **Results.** Each file may carry n8n's publishing error and a capped list of import policy findings; these
  texts, folder names, and project names are untrusted data, capped, and never part of an error message. Only
  the preview adds the Git path, folder, owner, and policy details; pull and push report the shorter view.
