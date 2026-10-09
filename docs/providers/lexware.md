---
description: >
  Describes Lexware reads of invoices, sales documents, recurring invoice templates, vouchers, contacts,
  articles, the organization profile and reference data, voucher document downloads, invoice, quotation and
  order confirmation drafts, invoice issuing, contact and article writes, article deletion, permissions, and
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

`lexware.documents.download` writes the final document of a finalized sales voucher and
`lexware.files.download` the original file of a bookkeeping voucher to `local_path`. Both are offered only to a
connection that releases a directory under `files.write`, replace an existing file only with confirmation, stop
at 20 MiB, and return only identifier, size and SHA-256. The format `default` takes Lexware's own choice,
`pdf` and `xml` select a representation; `xml` exists for an XRechnung only. Lexware generates the PDF of an
XRechnung solely as a preview, not as a valid e-invoice. A draft has no document until it is finalized.

A connection may carry one optional target `organization/<uuid>` (the `organizationId` of the profile). It
protects connections whose keys belong to different customers: before the first request of a tool, Qatlas
reads the profile of the key once per process and refuses a key of another organization as `permission`,
without sending the request and without naming either organization. A failed profile read blocks the request
with its own error class and is retried by the next call. The connection test reports the same refusal as
`permission`. Without a target nothing is checked and the organization follows from the key. After a
successful connection test of an unbound connection, the terminal editor offers the organization of the key as
a prefilled target in the connection form; it is saved only when you save the form.

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

`lexware.articles.delete` deletes one article for good (`delete`), always with confirmation. Like the issue
tool it is offered only when a connection's `tools` list names it, in addition to `delete` in `permissions`;
no profile ticks it. A repeated call reports the article as missing; after an unclear outcome the error says
that the article may have been deleted.

`lexware.quotations.create` (with a required `expiration_date`) and `lexware.orderconfirmations.create` create a
quotation and an order confirmation as drafts (`create`), always with confirmation, and share the voucher
structure of `lexware.invoices.create`. Neither finalizes, and there is no issue tool for them. An order
confirmation may follow up a quotation through `preceding_voucher_id`; Lexware refuses a quotation that cannot be
followed up, for example one with optional positions, and the error says so without the provider's text. Besides
`custom` and `text`, positions may have the type `service` or `material` and then reference an article by its
`id`; they carry quantity, unit and price like `custom`. All three tools accept the optional `payment_conditions`
(label, duration, optional discount) and `print_layout_id`. Every identifier is checked as a UUID before any
request.

Breaking change: `lexware.invoices.create` version 2 no longer accepts `finalize` and rejects it as an unknown
argument; its result no longer carries `finalized`. A connection that finalized invoices through `create`
needs `lexware.invoices.issue` in its `tools` list instead.

Each creation or issue sends exactly one request and is never repeated, also not after a rate limit. When its
outcome is unclear, such as after a timeout, a dropped connection, a server error or an unusable answer, the
error says that the invoice may have been created or issued; check the voucher list before repeating it.

`lexware.articles.create` and `lexware.contacts.create` create a product or service and a customer or vendor
(`create`); `lexware.articles.update` and `lexware.contacts.update` change named fields of one (`update`). All
four always need confirmation and take closed, length-bounded fields. An article price is given as the leading
amount (`NET` or `GROSS`) with its tax rate, and Lexware derives the other amount. A contact is a person or a
company, holds the roles customer and vendor, and carries at most one billing and one shipping address, one
e-mail address per kind, one phone number per kind and, for a company, one contact person.
An update reads the object, replaces only the given fields, keeps every other member the way Lexware
returned it, and writes it back with the version it read. If someone else changed it in between, Lexware
refuses the write and the error reports a conflict; Qatlas never overwrites it and never retries, so read the
object again and repeat the change deliberately. A contact update can add a role but not remove one, cannot
turn a person into a company or the reverse, and refuses a contact that holds several entries in one of those
lists before writing anything, because Lexware would drop the extra entries. A rejection of the data
(HTTP 406) is reported without the provider's text. A failed read before the write changes nothing. After an
unclear outcome of the write the error says the object may have been created or changed; read it before
repeating.

Lexware's public invoice API does not expose update or delete endpoints, so Qatlas does not invent uniform
CRUD operations. The credential provides `api-key`; local connection permissions may restrict that key but
never extend its Lexware contract or organization rights. An optional `tools` list narrows a connection
further to named tools, for example `[lexware.invoices.get]`, and never admits an effect `permissions`
excludes. The terminal editor starts a new connection on the recommended setup profile `read`, which ticks
`[read]` and every list and get tool; the profile `write` adds the permissions `create` and `update` and the
tools `lexware.invoices.create`, `lexware.quotations.create`, `lexware.orderconfirmations.create`,
`lexware.articles.create`, `lexware.articles.update`, `lexware.contacts.create` and `lexware.contacts.update`.
A profile is a visible starting selection, not a role: only the ticked `permissions` and `tools` are saved,
every tick can be changed before saving, and a saved connection never follows a profile.
