---
description: >
  Describes Lexware reads of invoices, sales documents, recurring invoice templates, vouchers, contacts,
  articles, the organization profile and reference data, invoice drafts and issuing, permissions, and
  credentials.
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
Quotations, order confirmations, credit notes, delivery notes, dunnings and down payment invoices each have
their own detail tool by identifier, with address, positions, totals, shipping and related vouchers; a delivery
note carries no prices. A dunning names the invoice it refers to; a down payment invoice names its closing
invoice once one exists. Templates for recurring invoices are listed page by page, sorted by one of a few fixed
properties, and read by identifier, with interval, next execution, execution status and whether the last
execution failed; the provider's error text of a failed execution is never shown.

`lexware.profile.get` reads the organization, its contract features and tax settings as their own data class;
the name, email and identifiers of the key creator are never returned. Countries, payment conditions, posting
categories and print layouts are fixed reference lists; a list longer than its cap is cut and marked
`truncated`. Print layouts need the Lexware contract scope `INVOICING_PRO`; without it the call fails as a
missing contract scope.

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
