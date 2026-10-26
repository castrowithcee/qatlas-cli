---
description: >
  Describes Nextcloud CardDAV tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Nextcloud contacts

Group `contacts`: address books and contacts.

The group `contacts` (profile `contacts`) reads address books and contacts (untrusted personal data) under the contact
[limits](../infomaniak.dav/README.md#contacts); the list reports the same fields per contact as the get, and neither requests
photo, logo, sound, key, or agent data, as it would come embedded. The system address book needs its own `addressbook/URI` target and is never written or shared. Writing is in
no profile; deleting and sharing need a tools list. A contact update replaces by `etag`, refusing a card it cannot write
whole. A new address book needs the general `addressbook` target; changing, deleting (with all contacts), or sharing one
needs it bound and owned. Sharing: one user or group, never link, e-mail, or team; unknown ones are ignored (`shares`).
Vendor properties of an address book are not standards and are not read; only `oc:invite` is, as `shares`.

Contact filters only narrow the answer; every href keeps its address book binding, and the list never sends
`nresults`: all hits are sorted before the cut at `limit`. An unsupported collation is a request error;
`contacts.address-books.list` names the supported ones. An `addressbook-query` holds a single filter, so with
`property_filters` a `query` is applied to the answer instead of the request.
