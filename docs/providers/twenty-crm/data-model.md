---
description: >
  Describes the Twenty CRM tools that read and change the objects and fields of the data model.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Twenty CRM: data model

## Data model

`meta-objects.list`, `meta-objects.get`, and `meta-fields.get` (`read`) are workspace-wide and read Twenty's
metadata API for all objects and fields. `isUIReadOnly` is marked deprecated in Twenty (replaced by
`isUIEditable`) and is left out. `defaultValue` and `settings` are free objects; one beyond the size limit is
left out and named in `omitted_fields`.

`meta-objects.create`, `update`, `delete` and `meta-fields.create`, `update`, `delete` change the schema for
every user and integration of the workspace. Rights: [Data model](setup.md#data-model). The writers need a `tools`
entry; no profile ticks them.
`meta-fields.create` adds a field to any object Twenty accepts, standard or custom; Twenty decides, Qatlas
does not pre-check the object. A field added to a standard object is a custom field.
`meta-objects.update` and `meta-fields.update` change standard and system objects and fields too, with every
argument Twenty accepts there; Twenty refuses the rest. Replacing the options of a field, a standard one
included, clears every removed value in all records.

`meta-fields.create` with the type `RELATION` links two objects and creates one field on each, so it changes the
schema of both; the target may be any object Twenty accepts, workspace members included. `meta-fields.update`
changes label, description, and active state of a relation field; `meta-fields.delete` removes a deactivated
custom relation field together with its counterpart on the other object and the links of all records. Morph
relations cannot be created, changed, or deleted.
