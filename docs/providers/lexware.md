---
description: >
  Describes Lexware invoice reads and creation, lifecycle limits, connection permissions, and credentials.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-23
---

# Lexware Office

A connection selects the organization associated with one API key at the fixed Lexware gateway. It lists
open and overdue outgoing invoices and reads invoice details (`read`). It can create an invoice as a draft
or finalize it immediately (`create`), always with confirmation.

Lexware's public invoice API does not expose update or delete endpoints. Finalized invoices are no longer
editable, so Qatlas does not invent uniform CRUD operations. The credential provides `api-key`; local
connection permissions may restrict that key but never extend its Lexware contract or organization rights.
An optional `tools` list narrows a connection further to named tools, for example
`[lexware.invoices.get]`, and never admits an effect `permissions` excludes. The terminal editor starts a new
connection on the setup profile `read`, which ticks `[read]` and `[lexware.invoices.list,
lexware.invoices.get]`. A profile is a visible starting selection, not a role: only the ticked `permissions`
and `tools` are saved, every tick can be changed before saving, and a saved connection never follows a
profile.
