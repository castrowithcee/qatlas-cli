---
description: >
  Describes the Matomo custom dimension tools: listing, options, adding, and updating a website's dimensions, their rights, limits, and unclear outcomes.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Matomo custom dimensions

The custom dimension tools of the [Matomo provider](README.md). They need the plugin CustomDimensions; an instance
without it reports the tools as unsupported. Every tool takes `idSite`, checked against the connection's targets
before any secret or request. `idDimension` counts per website, and Matomo looks it up with the website, so an ID of
another website is not found.

- `matomo.dimensions.list` reads the dimensions of a website with scope, slot index, state, extraction rules,
  case sensitivity, and description (view right). Names, descriptions, and patterns are untrusted provider data.
- `matomo.dimension-options.list` reads the scopes (`visit`, `action`) with available, used, and free slots, and the
  sources extraction rules can read. The sources need a write right on some website.
- `matomo.dimensions.add` creates a dimension (write right). It is not idempotent and uses a slot of its scope for
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
