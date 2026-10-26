---
description: >
  Describes the Twenty CRM tools that read and control workflows and workflow runs.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Twenty CRM: workflows

## Reading workflows

`workflows.list`, `workflows.get`, and `workflow-runs.list` (`read`) are workspace-wide.

- Step and trigger settings, outputs, context, state, step logs, and error texts of workflows and runs are not
  documented in their kind and can hold credentials (headers, tokens, webhook addresses). They count as
  credentials and are never read out.
- `workflows.list` filters by a name part, matched as plain text: `%`, `_`, and `"` are masked, and a backslash is
  refused. A cursor continues only the list of the same filters.
- A workflow run cannot be started with an API key: Twenty refuses it, and Qatlas does not work around that.

## Controlling workflows

`workflow-versions.activate`, `deactivate` (`update`), `workflow-runs.stop`, and `retry` (`execute`) are
workspace-wide and need a `tools` entry; no profile ticks them. Rights: [Workflows](setup.md#workflows).

- `activate` reads the version first and activates it only if it opens no lasting path to the outside through
  Qatlas: a webhook trigger or a step that can send data out or run code is refused before any change. The
  check rests on trigger and step types alone, from a fixed positive list; activate other versions in Twenty.
  The version can change between the read and the activation.
- `retry` runs the steps of the run again, including steps with effects outside Twenty.
