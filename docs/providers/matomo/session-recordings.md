---
description: >
  Describes the Matomo session recording tools: listing and reading session recording configurations, listing the recorded sessions of a recording, reading one recorded session with the event types, the personal data they hold, the typed text that is never returned, the removal of the token from replay and page addresses, the two-step binding of a recording to the connection's targets, the plugin requirement, and the limits of what the HeatmapSessionRecording reference proves.
type: knowledge
edit: shared
created: 2026-10-31
updated: 2026-10-31
---

# Matomo session recordings

The session recording tools of the [Matomo provider](README.md). They need the premium plugin
HeatmapSessionRecording, like the [heatmap tools](heatmaps.md); an instance without it refuses all of them as an
unsupported capability, after one plugin check and without further requests. The tools only read and are in no
[setup profile](README.md#setup-profiles). Reports of the plugin are read with `matomo.reports.get`.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.session-recordings.list` | reads the session recording configurations of a website, in batches |
| `matomo.session-recordings.get` | reads one session recording configuration of a website |
| `matomo.recorded-sessions.list` | lists the recorded sessions of one recording in one period, in batches |
| `matomo.recorded-sessions.get` | reads the events of one recorded session and the names of the event types |

- `matomo.recorded-sessions.list` takes a single period (day, week, month, year, or `range` with a range of dates),
  optionally a segment, and optionally `idVisit` for all recorded page views of one visit. `idVisit` is sent as the
  `idSubtable` of Matomo.
- `matomo.recorded-sessions.get` asks `getRecordedSession` and `getEventTypes`. When Matomo answers with a list, the
  events come in batches in `events`; when it answers with an object, it is returned as `session` and its lists
  are shortened to 100 entries, strings and nesting are shortened too.
- Names, page rules, pages, selectors, and addresses are untrusted provider data. They are shortened, an address
  is returned as text and never fetched, and a configuration shows at most 100 page rules.

## Personal data

The recorded sessions show what a person did on a website: visited pages, location, device, visitor ID, and
behavior. The two `matomo.recorded-sessions.*` tools therefore carry their own data sensitivity, apart from the
configuration tools and the heatmap tools. Recordings may capture what visitors type (`capture_keystrokes` is on by
default in Matomo). The tools take the narrower reading: events of the types Form Text Change (9) and Form Value
Change (10) keep only their type, time, and selector, and `typed_text_withheld` counts them. Typed text is not
returned even when the recording captured it. Other event content (page structure and changes, clicks, scrolling)
is returned shortened.

## Tokens in addresses

Matomo puts the token of the caller into `sessionReplayUrl` as the parameter `token_auth`. The removal is the one
described for [heatmap addresses](heatmaps.md#tokens-in-addresses). The visited pages (`first_url`, `last_url`)
come from the tracked website and are not an address Matomo adds the token to; they take the same removal when they
name a token and are otherwise returned unchanged.

## Targets

All tools take `idSite`, which must be inside the targets ([Scope](README.md#scope)) and is checked before any
secret is read. A recording ID is not tied to a website. `matomo.session-recordings.get` proves the recording by the
website the answer carries; a recording of another website, or an answer without a website, is not shown and ends as
`not-found`, naming no other website. In a list, such rows are dropped. Both `matomo.recorded-sessions.*` tools read
the configuration of the recording first as this proof and read recorded data only when the recording belongs to the
website. A row of `matomo.recorded-sessions.list` whose replay address names another website or recording is
dropped, and an object answer of `matomo.recorded-sessions.get` that names another website, recording, or session
is refused. The rows and events otherwise name neither, so the binding rests on the proof read.

## Limits

- The rights are not checked in advance. A view right on the website is expected for all four tools, as the public
  demo shows for the lists; the reference does not state it for the recorded sessions.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5
  (HeatmapSessionRecording 5.x); the changelog shows no signature change.
- The public demo has no recordings, so the answers of `getRecordedSessions` and `getSessionRecording` follow the
  reference examples. The reference gives `getRecordedSession` no example: its answer form, the names of its event
  members, and the event type member `event_type` that identifies form events are assumptions. An event
  object whose type cannot be read as a number loses its `text` and `value` members and is counted as well.
- The reference shows `idvisitor` as `false` or empty for visits without a visitor ID; such values are returned as
  no visitor ID. The unit of `time_on_site` is not documented and the value is returned as Matomo writes it.
- `getEmbedSessionInfo` appears only in the legacy reference without a description and is not offered.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; the text of an unknown recording is the one the public demo gives and ends as `not-found`.
