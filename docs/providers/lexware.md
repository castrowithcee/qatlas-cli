---
description: >
  Describes Lexware reads of invoices, vouchers, contacts, articles, organization profile and reference data, invoice drafts and issuing,
  permissions, and credentials.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-09
---

# Lexware Office

A connection selects the organization associated with one API key at the fixed Lexware gateway. It lists open
and overdue outgoing invoices and reads invoice details (`read`). It also finds customers, vendors and
articles and reads their details (`read`), for example to reference a `contact_id` in an invoice. The `name`
and `email` filters of the contact list need at least 3 characters and match a literal part of the value:
Lexware's placeholders `_` and `%` are escaped. Contacts are personal data. The voucher list searches vouchers
of every type and status; without a type or status filter it includes all of them. `any` and the status
`overdue` each stand alone in their filter. Lexware lists at most 10,000 vouchers per filter, so a page beyond
that limit is refused before any request; narrow the filter instead. It also reads the payment status of one
voucher and one bookkeeping voucher with its positions; bookkeeping vouchers are their own data class.

`lexware.profile.get` reads the organization, contract features and tax settings of the API key's organization
(data class `lexware-account-data`); the name, email and identifiers of the key creator are never returned.
`lexware.countries.list`, `lexware.paymentconditions.list`, `lexware.postingcategories.list` and
`lexware.printlayouts.list` read fixed reference lists (data class `lexware-reference-data`); only the posting
categories accept a local `type` filter. Countries are capped at 500 entries, the other lists at 200; a longer
answer is cut and marked `truncated`. Print layouts need the contract scope `INVOICING_PRO`; without it Lexware
refuses the call and the error names a missing contract scope.

`lexware.invoices.create` creates an invoice as a draft without an invoice number (`create`), always with
confirmation. Issuing an invoice is the separate tool `lexware.invoices.issue`: Lexware then assigns the
invoice number, and its API can neither change nor delete the invoice afterwards. A connection offers
`lexware.invoices.issue` only when its `tools` list names it, in addition to `create` in `permissions`; no
profile ticks it.

Breaking change: `lexware.invoices.create` version 2 no longer accepts `finalize` and rejects it as an unknown
argument; its result no longer carries `finalized`. A connection that finalized invoices through `create`
needs `lexware.invoices.issue` in its `tools` list instead.

Each creation or issue sends exactly one request and is never repeated, also not after a rate limit. When its
outcome is unclear, such as after a timeout, a dropped connection, a server error or an unusable answer, the
error says that the invoice may have been created or issued; check the voucher list before repeating it.

Lexware's public invoice API does not expose update or delete endpoints, so Qatlas does not invent uniform
CRUD operations. The credential provides `api-key`; local connection permissions may restrict that key but
never extend its Lexware contract or organization rights. An optional `tools` list narrows a connection
further to named tools, for example `[lexware.invoices.get]`, and never admits an effect `permissions`
excludes. The terminal editor starts a new connection on the recommended setup profile `read`, which ticks
`[read]` and every list and get tool; the profile `write` adds `create` and `lexware.invoices.create`. A
profile is a visible starting selection, not a role: only the ticked `permissions` and `tools` are saved,
every tick can be changed before saving, and a saved connection never follows a profile.
