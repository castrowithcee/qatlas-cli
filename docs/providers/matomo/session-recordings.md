---
description: >
  Describes the Matomo session recording tools: listing, reading, creating, and replacing session recording configurations, pausing, resuming, and ending a recording, the typed-text setting of a configuration, listing the recorded sessions of a recording, reading one recorded session with the event types, deleting a recording, a recorded session, or a recorded page view, the personal data they hold, the typed text that is never returned, the removal of the token from replay and page addresses, the two-step binding of a recording to the connection's targets, the plugin requirement, and the limits of what the HeatmapSessionRecording reference proves.
type: knowledge
edit: shared
created: 2026-10-31
updated: 2026-10-31
---

# Matomo session recordings

The session recording tools of the [Matomo provider](README.md). They need the premium plugin
HeatmapSessionRecording, like the [heatmap tools](heatmaps.md); an instance without it refuses all of them as an
unsupported capability, after one plugin check and without further requests. The change tools and the read tools are in no
[setup profile](README.md#setup-profiles); the delete tools are offered only via `tools`. Reports of the plugin are
read with `matomo.reports.get`.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.session-recordings.list` | reads the session recording configurations of a website, in batches |
| `matomo.session-recordings.get` | reads one session recording configuration of a website |
| `matomo.recorded-sessions.list` | lists the recorded sessions of one recording in one period, in batches |
| `matomo.recorded-sessions.get` | reads the events of one recorded session and the names of the event types |
| `matomo.session-recordings.add` | creates a session recording configuration (effect `create`) |
| `matomo.session-recordings.update` | replaces a session recording configuration (effect `update`) |
| `matomo.session-recordings.pause` | pauses a session recording (effect `update`) |
| `matomo.session-recordings.resume` | resumes a paused session recording (effect `update`) |
| `matomo.session-recordings.end` | ends a session recording (effect `update`) |
| `matomo.session-recordings.delete` | deletes a session recording (effect `delete`, only via `tools`) |
| `matomo.recorded-sessions.delete-session` | deletes all recorded page views within one recorded session of a visit (effect `delete`, only via `tools`) |
| `matomo.recorded-sessions.delete-pageview` | deletes one recorded page view within a recorded session (effect `delete`, only via `tools`) |

- `matomo.recorded-sessions.list` takes a single period (day, week, month, year, or `range` with a range of dates),
  optionally a segment, and optionally `idVisit` for all recorded page views of one visit. `idVisit` is sent as the
  `idSubtable` of Matomo.
- `matomo.recorded-sessions.get` asks `getRecordedSession` and `getEventTypes`. When Matomo answers with a list, the
  events come in batches in `events`; when it answers with an object, it is returned as `session` and its lists
  are shortened to 100 entries, strings and nesting are shortened too.
- Names, page rules, pages, selectors, and addresses are untrusted provider data. They are shortened, an address
  is returned as text and never fetched, and a configuration shows at most 100 page rules.
- `matomo.session-recordings.add` and `matomo.session-recordings.update` need confirmation and send exactly one
  request that is never repeated after an unclear result; they take `name` (required) and every optional setting
  the reference lists. A setting that is not given is not sent, so Matomo's own defaults apply (by the signature, no
  page rules, `sampleLimit` 1000, `sampleRate` 10, `minSessionTime` 0, `requiresActivity` and `captureKeystrokes`
  on, `autoRepeat` off). `matchPageRules` is optional, as the signature defaults it to none; a given list holds 1
  to 20 rules. `description` and `autoRepeat` appear only in the legacy reference.
- `captureKeystrokes` is on by default in Matomo, which then records what visitors type into forms and leaves
  password fields out. The argument description says so; a recording is never created with it on by a Qatlas
  default, only by Matomo's.
- `matomo.session-recordings.update` is a full replacement in effect: a setting left out may reset a value the
  recording had to Matomo's default. Whether Matomo resets them is not verified. Nothing is filled in from the
  current configuration; read it with `matomo.session-recordings.get` and pass what is to stay. It is idempotent;
  `matomo.session-recordings.add` is not, and a repeated call creates a second recording.

- `matomo.session-recordings.pause`, `matomo.session-recordings.resume`, and `matomo.session-recordings.end` take only
  `idSite` and `idSiteHsr` and change the state of the recording, not the configuration or the recorded sessions.
  The reference does not say whether an ended recording can be resumed. All three need confirmation, send exactly one
  request that is never repeated after an unclear result, and are idempotent in effect.
- `matomo.session-recordings.delete` removes a recording; Qatlas offers no way to restore it, and the reference does not
  say what happens to its recorded sessions. `matomo.recorded-sessions.delete-session` (with `idVisit`) and
  `matomo.recorded-sessions.delete-pageview` (with `idLogHsr`) remove recorded data of one visitor visit and carry the
  data sensitivity of the recorded sessions. They are offered only where a connection's `tools` list names them, need
  confirmation, send exactly one request that is never repeated after an unclear result, and are idempotent in effect.
  They take the IDs from `matomo.recorded-sessions.list`.

## Personal data

The recorded sessions show what a person did on a website: visited pages, location, device, visitor ID, and
behavior. The four `matomo.recorded-sessions.*` tools therefore carry their own data sensitivity, apart from the
configuration tools and the heatmap tools. The configuration tools, including the ones that write, carry the
configuration sensitivity. Recordings may capture what visitors type (`capture_keystrokes` is on by
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

`matomo.session-recordings.update`, `.pause`, `.resume`, `.end`, and `.delete`, and both
`matomo.recorded-sessions.delete-*` tools read the configuration first with the same proof and send their change only
when the recording belongs to the website. The two recorded-session deletions send `idVisit` or `idLogHsr` together
with `idSite` and `idSiteHsr` and take no second read: the binding of that ID rests on the proof read and on the
assumption, which the public sources cannot confirm, that Matomo resolves it within the website and recording.
`matomo.session-recordings.add` creates on the chosen website only.
All tools take `idSite`, which must be inside the targets ([Scope](README.md#scope)) and is checked before any
secret is read. A recording ID is not tied to a website. `matomo.session-recordings.get` proves the recording by the
website the answer carries; a recording of another website, or an answer without a website, is not shown and ends as
`not-found`, naming no other website. In a list, such rows are dropped. The two read tools
`matomo.recorded-sessions.list` and `.get` read the configuration of the recording first as this proof and read
recorded data only when the recording belongs to the website. A row of `matomo.recorded-sessions.list` whose replay
address names another website or recording is dropped, and an object answer of `matomo.recorded-sessions.get` that
names another website, recording, or session is refused. The rows and events otherwise name neither, so the binding
rests on the proof read.

## Limits

- The rights are not checked in advance. A view right on the website is expected for the four read tools, as the
  public demo shows for the lists; the reference does not state it for the recorded sessions. A write right is
  expected for the eight change tools; the public sources do not state it.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5
  (HeatmapSessionRecording 5.x); the changelog shows no signature change.
- The public demo has no recordings, so the answers of `getRecordedSessions` and `getSessionRecording` follow the
  reference examples. The reference gives `getRecordedSession` no example: its answer form, the names of its event
  members, and the event type member `event_type` that identifies form events are assumptions. An event
  object whose type cannot be read as a number loses its `text` and `value` members and is counted as well.
- The reference shows `idvisitor` as `false` or empty for visits without a visitor ID; such values are returned as
  no visitor ID. The unit of `time_on_site` is not documented and the value is returned as Matomo writes it.
- The reference gives `addSessionRecording` an ID as answer and `updateSessionRecording`, `pauseSessionRecording`,
  `resumeSessionRecording`, `endSessionRecording`, `deleteSessionRecording`, `deleteRecordedSession`, and
  `deleteRecordedPageview` the generic success form, without an example (its exact JSON form is an assumption); the
  tools take the ID or the success form. The limits of 255 characters for name and
  description, 0.1 to 100 for `sampleRate`, and 20 page rules are Qatlas's own.
- `getEmbedSessionInfo` appears only in the legacy reference without a description and is not offered.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; the text of an unknown recording is the one the public demo gives and ends as `not-found`.
