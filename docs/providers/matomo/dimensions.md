---
description: >
  Describes the Matomo custom dimension tools: listing, options, adding, and updating a website's dimensions, their limits and unclear outcomes.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo custom dimensions

The custom dimension tools of the [Matomo provider](README.md). The rights and the plugin are listed in
[rights per tool group](setup.md#rights-per-tool-group); an instance without the plugin reports the tools as unsupported.
Every tool takes `idSite`, checked against the connection's targets
before any secret or request. `idDimension` counts per website, and Matomo looks it up with the website, so an ID of
another website is not found.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.dimensions.list` | reads the custom dimensions of a website |
| `matomo.dimension-options.list` | reads the scopes with free slots and the extraction sources |
| `matomo.dimensions.add` | adds a custom dimension, using a slot for good (effect `create`) |
| `matomo.dimensions.update` | changes or pauses a custom dimension; Matomo cannot delete one (effect `update`) |

- `matomo.dimensions.list` reads the dimensions of a website with scope, slot index, state, extraction rules,
  case sensitivity, and description. Names, descriptions, and patterns are untrusted provider data.
- `matomo.dimension-options.list` reads the scopes (`visit`, `action`) with available, used, and free slots, and the
  sources extraction rules can read.
- `matomo.dimensions.add` creates a dimension. It is not idempotent and uses a slot of its scope for
  good, because Matomo cannot delete a dimension.
- `matomo.dimensions.update` changes name, state, rules, case sensitivity, or description. `active=false` pauses a
  dimension; there is no deletion. The scope cannot be changed after creation. Matomo replaces name, state, and rules
  as a whole, so Qatlas reads the dimension once and sends the current values for what is not given; this read also
  binds the ID to the website. `extractions` is the complete new list; an empty list clears it.

A name holds 1 to 255 characters without `/ \ & . < >`; a description at most 1000 characters without markup.
Extraction rules exist only for the scope `action`, at most 10 rules per dimension and 255 characters per pattern
(Qatlas caps); the sources are `url`, `urlparam`, and `action_name`. Reports of a dimension are read with
`matomo.reports.get`.

Every change needs the confirmation of the caller and is sent exactly once. After an unclear result the error says
the change may have been applied and is never repeated; read the dimensions with `matomo.dimensions.list` first.
