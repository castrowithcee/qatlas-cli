---
description: >
  Describes the Infomaniak Calendar and Contacts provider: read-only CalDAV and CardDAV access against the
  fixed Infomaniak Sync host with a user name and application password, the required calendar and address
  book allow-list, the list tools, the two event read tools, the path binding, the response caps, and what
  the provider does not do.
type: knowledge
edit: shared
created: 2026-10-02
updated: 2026-10-04
---

# Infomaniak Calendar and Contacts

Infomaniak Calendar and Contacts is a provider for the CalDAV and CardDAV service of one Infomaniak identity.
It lists the calendars and address books the connection allows and reads the events of the allowed
calendars. It is read-only: it reads no contact, and it creates, changes, or deletes nothing.

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
| `infomaniakdav.events.list` | read | none | the events of one allow-listed calendar in a time range |
| `infomaniakdav.events.get` | read | none | one event of an allow-listed calendar |

The two collection lists take no argument. The one recommended profile, `read`, offers all four tools. A
connection needs the `read` permission.

Each collection carries `id`, `name`, `description`, and, for a calendar, `color` when the server reports a
valid hex value.

## Events

Both event tools take `calendar`, the ID of a `calendar/ID` target. A calendar outside the allow-list is
refused as `permission` before any secret is read or any request is sent, and the message does not name it.

`infomaniakdav.events.list` takes the required `start` and `end` as RFC 3339 times; `end` must be after
`start` and at most 366 days later. They are converted to UTC for a CalDAV `calendar-query` REPORT of the
`VEVENT` components that overlap the range. `limit` defaults to 50 and may be at most 200. Events are sorted
by start and cut at `limit`; `truncated` is true when more matched. The list is compact: `id`, `uid`,
`summary`, `location`, `start`, `end`, `all_day`, `timezone`, `status`, `rrule`, and `etag`.

`infomaniakdav.events.get` takes the required `id`, the event as the list reports it, and reads that one
event with a GET. Besides the compact members it returns `description`, `organizer`, and at most 50
`attendees` (`attendees_truncated` when there were more). The `etag` is the entity tag of the event version.

A repeating event is reported once with its recurrence rule as `rrule`; it is never expanded into
occurrences, and overrides of single occurrences are not reported. `start` and `end` are a date for an
all-day event, a UTC time ending in `Z`, or a local time with the zone name in `timezone`; zones are not
resolved. The `id` is one literal path segment under the calendar, with the same rules as a target ID,
and is checked before any request. Every event href of an answer must be a direct child of the allowed
calendar, otherwise the whole answer is refused as an invalid response. The list leaves out an event whose
data is not a valid iCalendar object.

## Discovery and path binding

Each list runs three PROPFIND requests, all of depth 0 except the last: `current-user-principal` of the root,
then `calendar-home-set` or `addressbook-home-set` of that principal, then one depth 1 listing of the home
set. An event tool runs the first two, then one REPORT of the allowed calendar or one GET of one event in it.
Only these three methods are ever sent, over one internal request path, and never one that an argument names.
Request bodies are fixed or built from validated values.

Every location the server names is untrusted. It must be a path on the fixed origin, without a query,
fragment, or userinfo, and without an empty, `.`, or `..` component, also not percent-encoded. A discovery
location must be named exactly once and may not be the root. A collection in the listing must be a direct
child of the discovered home set; any other node, including one on another host, refuses the whole answer as
an invalid response.

## Limits

A multi-status answer is read up to 1 MiB, at most 500 nodes, nested at most 16 levels, with text capped at 4
KiB per element, except one event, which is refused beyond 64 KiB instead of cut. A GET answer is read up
to 64 KiB. A range that matches more than 500 events or more than 1 MiB of answer is refused as an invalid
response: narrow the range. Event summaries are capped at 256 bytes, descriptions at 4096, locations at 256,
the `rrule` at 512; control characters are replaced, except line breaks in a description. Collection names
are capped at 256 bytes and descriptions at 1024 bytes, with control characters replaced. An answer beyond a
cap is refused instead of truncated.

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
sensitivity class `infomaniak-dav-collections`. Everything in an event, such as summary, description,
location, organizer, and attendees, is untrusted data of the class `infomaniak-dav-events`. Qatlas bounds
them and never renders, follows, or executes anything derived from them.

## Boundary

This provider lists calendars and address books and reads events. It does not write events, read contacts,
create, rename, share, or delete collections, or accept a free URL, method, or body from a tool argument.
