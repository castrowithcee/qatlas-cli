---
description: >
  Describes the n8n credential tools: metadata-only reads, project binding by a bounded search, the test
  tool's own profile, plain data against secret values passed only by reference to a forward credential, and
  the owner rule for updating, moving, and deleting credentials.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# n8n credentials

Configuration, targets, error classes, and cross-group limits are in [README.md](README.md); the retry
contract of every change is in [workflows.md](workflows.md#changes-and-their-retry-contract).

## Profiles

| Profile | Tools |
| --- | --- |
| `credentials-read` | `n8n.credentials.list`, `n8n.credentials.get`, `n8n.credentials.schema` |
| `credentials-test` | `n8n.credentials.test` |

These are in none of the other profiles. `n8n.credentials.create`, `.update`, `.transfer`, and `.delete` are
in no profile and offered only by a `tools` list.

## Reads and tests

The read tools expose metadata only, never a credential's data. Responses are decoded into structures
without a field for it, so a stored value never reaches a result or an error, whatever n8n sends.

- **Binding.** With a project allow-list `list` keeps only credentials shared into those projects. `get` and
  `test` first find the credential in the paged list (at most 20 pages of 250), because the single read has no
  project information; a credential that is not found, is outside the allow-list, or whose search cannot
  finish is refused without any further request and without naming a project. Each `shared` entry of the list
  response carries the project as its `id` (with `name` and `role`); the single read has no `shared`. Only
  that `id` is read for the binding; an entry without one never matches. A connection with a workflow
  allow-list refuses every credential tool locally, as for data tables and variables.
- **Sharing.** Shares of a credential in projects outside the project allow-list appear only with id and role,
  so a credential shared into an allowed project does not disclose the name or times of a foreign one. Without
  an allow-list every share is complete.
- **Test.** `credentials.test` makes n8n use the stored secret against the third-party service, so it is
  effect `execute`, idempotent, open-world, and always needs confirmation. It sends exactly one request and is
  never retried. The result is `status` and a capped `message`, untrusted data, never part of an error. It has
  its own profile so it can be granted separately from reading.
- **Schema.** `credential_type` is letters, digits, `.`, `-`, and `_` (at most 100 characters, starting with a
  letter or digit) and is sent as one escaped path segment. The result lists at most 200 fields, sorted.
  n8n reports per field only the type and, for an options field, its values (at most 200, flagged when cut);
  descriptions and defaults are not in its answer. The conditional required fields are a free object in
  `all_of`, bounded in entries and depth; one beyond the bounds is left out whole and flagged, never cut.
  `type` and `additionalProperties` of n8n's answer are constants per its specification and are not returned.

## Changes

- **Create.** `data` is built from the plain `data` argument (an object of at most 50 fields; names of
  letters, digits, `_`, `-`, `.`, starting with a letter; values strings of at most 1024 characters, numbers,
  or booleans; never null, objects, or arrays) plus the fields of the `secret_ref`. At least one of them is
  required. With a project allow-list `project_id` is required and must be one of its projects; without one it
  is optional and n8n defaults to the API key owner's personal project. A repeated call creates another
  credential.
- **Update.** The type cannot be changed. An update with `data` or `secret_ref` **replaces the whole stored
  data**: Qatlas sends the merged `data` object without `isPartialData`, which n8n treats as false and
  replaces the entire data object. A field that is in neither `data` nor the referenced forward credential, a
  stored secret included, is lost, so give every plain field in `data` and every secret field through
  `secret_ref`. A rename (only `name`) sends no `data` and leaves the stored data unchanged. At least one of
  `name`, `data`, `secret_ref` is required.
- **Secret values.** A value is never an argument. `secret_ref` names a forward credential that the
  connection releases in `forward_secrets`; the core checks it before the confirmation gate and refuses an
  unreleased or unknown name (`secret-ref-not-allowed`). The values are read only after the confirmation, for
  the one request, registered with the redactor, and set into `data` under the field names of the forward
  credential. A plain `data` field with the same name as a secret field is refused, never overwritten.
  Because of `secret_ref`, `create` and `update` need a tools list entry, the `create` or `update`
  permission, and their own confirmation. The plain `data` argument is not redacted: put no secret into it.
- **Transfer.** It needs a project allow-list, the credential must be found in one of its projects (the same
  bounded search as `get`), and `destination_project_id` must be in the allow-list; otherwise it is refused,
  without a request where that can be decided locally. Workflows in other projects may lose access to the
  moved credential.
- **Delete** finds the credential through the same binding search. Workflows that use the credential fail
  until another one is assigned, and the stored secret is lost.
- **Owner.** Update, transfer, and delete additionally require the owning project (the `shared` entry with
  role `credential:owner`) to be in the allow-list; a credential only shared into an allowed project, or without
  a role, is refused before the changing request. The binding search before the one changing request only
  reads. Results are the credential metadata, or `deleted` and `transferred` flags; never a stored value.
- **403.** Reported as `permission`; the rights are in [rights per tool group](setup.md#rights-per-tool-group).
