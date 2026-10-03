---
description: >
  Describes the Infomaniak Calendar and Contacts provider: CalDAV and CardDAV access against the fixed
  Infomaniak Sync host with a user name and application password, the required calendar and address book
  allow-list, the list tools, the event and contact read tools, the confirmed event create, update, and delete
  tools, the path binding, the response caps, and what the provider does not do.
type: knowledge
edit: shared
created: 2026-10-02
updated: 2026-10-04
---

# Infomaniak Calendar and Contacts

Infomaniak Calendar and Contacts is a provider for the CalDAV and CardDAV service of one Infomaniak identity.
It lists the calendars and address books the connection allows, reads the events of the allowed calendars,
and creates, replaces, and deletes such events from structured fields. It lists and reads the contacts of the
allowed address books. It changes no contact and no collection.

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
| `infomaniakdav.contacts.list` | read | none | the contacts of one allow-listed address book |
| `infomaniakdav.contacts.get` | read | none | one contact of an allow-listed address book |
| `infomaniakdav.events.create` | create | required | a new event in an allow-listed calendar |
| `infomaniakdav.events.update` | update | required | replaces one event, bound to its etag |
| `infomaniakdav.events.delete` | delete | required | deletes one event, bound to its etag |

The two collection lists take no argument. The recommended profile, `read`, offers the six read tools and
needs the `read` permission. The profile `events` adds `events.create` and `events.update`
(permissions `create` and `update`); it is not recommended. `events.delete` belongs to no profile: a
connection offers it only when its `tools` list names it, besides the `delete` permission.

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

## Contacts

Both contact tools take `addressbook`, the ID of an `addressbook/ID` target. An address book outside the
allow-list is refused as `permission` before any secret is read or any request is sent, and the message does
not name it. Contacts are personal data and carry the data sensitivity class `infomaniak-dav-contacts`.

`infomaniakdav.contacts.list` sends one CardDAV `addressbook-query` REPORT with a fixed body. `limit` defaults
to 50 and may be at most 200. Contacts are sorted by name, then id, and cut at `limit`; `truncated` is true
when more were found. The list is compact: `id`, `uid`, `name`, the first `email`, `organization`, and `etag`.
A card that is not a valid vCard is left out of the list.

`infomaniakdav.contacts.get` takes the required `id`, the contact as the list reports it, and reads that one
contact with a GET. It returns `structured_name`, at most 20 `emails` and 20 `phones` with their `type`, at
most 20 `addresses`, `organization`, `title`, `birthday`, `note`, and at most 20 `urls`. A member cut at its
cap is named in `truncated`. A photo or other binary property is never returned. The `name` is the formatted
name, built from the structured name when the card has none. The `id` is one literal path segment under the
address book, checked before any request. Every contact href of an answer must be a direct child of the
allowed address book, otherwise the whole answer is refused as an invalid response.

## Writing events

The three write tools take `calendar` like the read tools and carry the risk `open_world: true` with data
sensitivity `infomaniak-dav-events`, because Infomaniak may send an invitation or an update to attendees. Each
needs `confirm: true`; without it nothing is sent. Every argument is validated before any secret is read or
any request is sent, and no error quotes a value.

Events are described by structured fields; no iCalendar text, header, method, or URL comes from an argument.
Qatlas builds the iCalendar object with the encoder of its iCalendar library, which escapes text, so a line
break or `;`, `,`, `\` in `summary`, `description`, or `location` stays text. Fields:

| Field | Rule |
| --- | --- |
| `summary` | required, at most 256 bytes |
| `description`, `location` | at most 4096 and 256 bytes; no control characters except line breaks |
| `start`, `end` | with `all_day`: dates `YYYY-MM-DD`, `end` exclusive and one day after `start` by default; otherwise `end` is required and both are local times `YYYY-MM-DDTHH:MM:SS` with `timezone`, or both UTC times ending in `Z`; `end` after `start`; no UTC offsets |
| `timezone` | an IANA name such as `Europe/Zurich`, checked against the zone database embedded in the binary, so the result does not depend on the host; sent as `TZID` without a `VTIMEZONE` component |
| `rrule` | a recurrence rule without the `RRULE:` prefix, at most 512 bytes, accepted only if the recurrence parser reads it; one series, never expanded |
| `status` | `TENTATIVE`, `CONFIRMED`, or `CANCELLED` |
| `organizer` | optional `{address, name}` |
| `attendees` | at most 50 `{address, name}`, each once; written as `mailto:` with `PARTSTAT=NEEDS-ACTION` |

An address must be a plain e-mail address (no display name, comment, or list); a name has no control character
or double quote. The generated object may be at most 64 KiB. A recurring event is only its main event:
overrides of single occurrences cannot be written.

`events.create` generates the UID and the resource id (a random UUID and `.ics`) itself and takes no `id`.
One PUT with `If-None-Match: *` stores the event, so no existing event can be replaced. The result holds
`id`, `uid`, and `etag` when Infomaniak reports one.

`events.update` replaces the event completely from the fields. The `etag` argument is required, as
`events.get` or `events.list` report it (one pair of surrounding quotes is accepted; `*`, weak tags, and
control characters are refused). Qatlas first reads the event with one GET to keep its UID and raise its
`SEQUENCE`, and refuses without writing if the entity tag no longer matches. It then sends exactly one PUT
with `If-Match`. Everything Qatlas does not model, such as alarms and other properties, is lost. An event with
overrides of single occurrences (`RECURRENCE-ID`), more than one event component, or other components than
events is refused before any write: change it in a calendar application.

`events.delete` sends one DELETE with `If-Match` and the required `etag`.

A changed entity tag (HTTP 412) is reported as a failed precondition with nothing changed: read the event
again. A write is sent once and never repeated. If its result stays open (timeout, interrupted connection,
a 5xx answer, an unexpected status), the error says the change may have taken effect: read the event before
repeating anything.

## Discovery and path binding

Each list runs three PROPFIND requests, all of depth 0 except the last: `current-user-principal` of the root,
then `calendar-home-set` or `addressbook-home-set` of that principal, then one depth 1 listing of the home
set. An event or contact tool runs the first two, then one REPORT of the allowed collection or one GET of
one item in it;
a write adds exactly one PUT or DELETE (and the update one GET before it).
Only these five methods are ever sent, over one internal read path and one internal write path, and never
one that an argument names.
Request bodies are fixed or built from validated values.

Every location the server names is untrusted. It must be a path on the fixed origin, without a query,
fragment, or userinfo, and without an empty, `.`, or `..` component, also not percent-encoded. A discovery
location must be named exactly once and may not be the root. A collection in the listing must be a direct
child of the discovered home set; any other node, including one on another host, refuses the whole answer as
an invalid response.

## Limits

A multi-status answer is read up to 1 MiB, at most 500 nodes, nested at most 16 levels, with text capped at 4
KiB per element, except one event or contact, which is refused beyond 64 KiB instead of cut. A GET answer is
read up to 64 KiB. A range that matches more than 500 events or more than 1 MiB of answer is refused as an
invalid response: narrow the range. Event summaries are capped at 256 bytes, descriptions at 4096, locations
at 256, the `rrule` at 512; contact names, e-mail addresses, phone numbers, and other text members at 256
bytes, notes at 4096; control characters are replaced, except line breaks in a description or note. Collection
names are capped at 256 bytes and descriptions at 1024 bytes, with control characters replaced. An answer
beyond a cap is refused instead of truncated.

## Errors

| Class | Cause |
| --- | --- |
| `auth` | the user name or application password was rejected or is unusable |
| `permission` | the identity may not read or change the location |
| `not-found` | the identity holds no such location |
| `unreachable`, `tls`, `timeout` | the server could not be reached, the TLS check failed, or it did not answer in 30 seconds |
| `rate-limited` | Infomaniak or a local limit asks to wait |
| `invalid-provider-response` | the answer broke the shape, bounds, or path binding above |
| `provider-error` | every other rejection, including a refused redirect, a failed precondition, and an event that cannot be replaced safely |

The text of an Infomaniak answer never reaches an error message, and neither does the password.

## Untrusted data

Names, descriptions, and colors come from the provider and are untrusted data. They carry the data sensitivity
class `infomaniak-dav-collections`. Everything in a contact is untrusted personal data of the class
`infomaniak-dav-contacts`. Everything in an event, such as summary, description, location, organizer, and
attendees, is untrusted data of the class `infomaniak-dav-events`. Qatlas bounds them and never renders,
follows, or executes anything derived from them.

## Boundary

This provider lists calendars and address books, reads events and contacts, and creates, replaces, and deletes
events of allow-listed calendars. It does not write contacts, create, rename, share, or delete collections,
expand recurrences, write overrides of single occurrences, or accept a free URL, method, header, or iCalendar
body from a tool argument.
