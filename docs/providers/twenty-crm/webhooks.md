---
description: >
  Describes the Twenty CRM tools that list, update, and delete webhooks.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Twenty CRM: webhooks

## Webhooks

`webhooks.list`, `get` (`read`), `update`, and `delete` show where the workspace reports events, change which
events it reports, and remove a webhook. There is no tool to create a webhook or to change its target or
signing key.

- The tools are workspace-wide. Rights: [Webhooks](setup.md#webhooks).
- The signing key is never shown, and the target is reduced to scheme, host with port, and path: query,
  fragment, and credentials are dropped, because they often carry tokens. The path stays visible, so webhook
  configuration has its own data class.
- `update` replaces the whole event list and/or the description. The input is checked before any request is
  sent, and the request contains nothing but these two fields. `delete` is offered only by a connection whose
  `tools` list names it.
