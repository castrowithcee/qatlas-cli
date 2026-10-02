---
description: >
  Describes the Infomaniak Calendar and Contacts provider: read-only CalDAV and CardDAV discovery against the
  fixed Infomaniak Sync host with a user name and application password, the required calendar and address
  book allow-list, the two list tools, the home-set path binding, the response caps, and what the provider
  does not do.
type: knowledge
edit: shared
created: 2026-10-02
updated: 2026-10-02
---

# Infomaniak Calendar and Contacts

Infomaniak Calendar and Contacts is a provider for the CalDAV and CardDAV service of one Infomaniak identity.
It lists the calendars and address books the connection allows. It is read-only: it reads no event and no
contact, and it creates, changes, or deletes nothing.

## Configuration

The origin is fixed to `https://sync.infomaniak.com`
([Infomaniak's sync settings](https://www.infomaniak.com/en/support/faq/2432/sync-your-contacts-and-calendars-across-all-your-devices)).
`base_url` may be left out; if it is set, it must be exactly that origin. No redirect is ever followed, so
the credential never leaves the origin.

The credential provides two roles:

| Role | Value |
| --- | --- |
| `user-id` | the personal user name of the sync identity, for example `AB12345` |
| `app-password` | an application password created in the Infomaniak Manager for this identity |

```yaml
services:
  infomaniak-dav:
    provider: infomaniakdav

credentials:
  customer-a-dav:
    provider: infomaniakdav
    type: keyring
```

## Scope

| Target | Binds |
| --- | --- |
| `calendar/ID` | one calendar; repeatable, at most 100 |
| `addressbook/ID` | one address book; repeatable, at most 100 |

`ID` is the last path segment of the collection below its home set, for example `work` for
`/calendars/AB12345/work/`. It is one literal segment of at most 128 bytes: no separator, no percent sign, no
`.` or `..`, no control character. Matching is exact and case-sensitive; there are no wildcards.

```yaml
connections:
  customer-a-dav:
    service: infomaniak-dav
    credential: customer-a-dav
    targets: [calendar/work, calendar/team, addressbook/default]
```

The allow-list is required: a connection needs at least one target, and a target named twice within its kind
is refused. Only listed collections are reported. If a connection lists only calendars,
`infomaniakdav.addressbooks.list` returns an empty set (and the other way round) without contacting
Infomaniak or reading a secret. A listed collection the server does not show is simply absent. No error
quotes a configured value.

## Tools

| Tool | Effect | Confirmation | Does |
| --- | --- | --- | --- |
| `infomaniakdav.calendars.list` | read | none | the allow-listed calendars |
| `infomaniakdav.addressbooks.list` | read | none | the allow-listed address books |

Neither tool takes an argument. The one recommended profile, `read`, offers both. A connection needs the
`read` permission.

Each collection carries `id`, `name`, `description`, and, for a calendar, `color` when the server reports a
valid hex value.

## Discovery and path binding

Each list runs three PROPFIND requests, all of depth 0 except the last: `current-user-principal` of the root,
then `calendar-home-set` or `addressbook-home-set` of that principal, then one depth 1 listing of the home
set. Only PROPFIND is ever sent, with fixed request bodies.

Every location the server names is untrusted. It must be a path on the fixed origin, without a query,
fragment, or userinfo, and without an empty, `.`, or `..` component, also not percent-encoded. A discovery
location must be named exactly once and may not be the root. A collection in the listing must be a direct
child of the discovered home set; any other node, including one on another host, refuses the whole answer as
an invalid response.

## Limits

A multi-status answer is read up to 1 MiB, at most 500 nodes, nested at most 16 levels, with text capped at 4
KiB per element. Names are capped at 256 bytes and descriptions at 1024 bytes, with control characters
replaced. An answer beyond a cap is refused instead of truncated.

## Errors

| Class | Cause |
| --- | --- |
| `auth` | the user name or application password was rejected or is unusable |
| `permission` | the identity may not read the location |
| `not-found` | the identity holds no such location |
| `unreachable`, `tls`, `timeout` | the server could not be reached, the TLS check failed, or it did not answer in 30 seconds |
| `rate-limited` | Infomaniak or a local limit asks to wait |
| `invalid-provider-response` | the answer broke the shape, bounds, or path binding above |
| `provider-error` | every other rejection, including a refused redirect |

The text of an Infomaniak answer never reaches an error message, and neither does the password.

## Untrusted data

Names, descriptions, and colors come from the provider and are untrusted data. They carry the data
sensitivity class `infomaniak-dav-collections`. Qatlas bounds them and never renders, follows, or executes
anything derived from them.

## Boundary

This provider lists calendars and address books. It does not read or write events or contacts, create,
rename, share, or delete collections, or accept a free URL, method, or body from a tool argument.
