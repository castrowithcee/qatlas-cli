---
description: >
  Describes the Matomo scheduled-report tools: listing reports with their recipients, rendering one as text, pausing one, and deleting one, the binding of an instance-wide report ID to the connection's targets, and the deliberately missing create, resume, and send tools.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Matomo scheduled reports

The scheduled-report tools of the [Matomo provider](README.md). The profiles are described under
[Setup profiles](README.md#setup-profiles). All four tools need the ScheduledReports plugin.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.scheduled-reports.list` | reads the scheduled e-mail reports inside the targets with their recipients (profile `live`) |
| `matomo.scheduled-reports.generate` | renders one scheduled report as html, csv, or tsv text, sent to no one (profile `live`) |
| `matomo.scheduled-reports.pause` | pauses one scheduled report by setting its schedule to never (creator or Super User; effect `update`, in no profile) |
| `matomo.scheduled-reports.delete` | deletes one scheduled report (creator or Super User; effect `delete`, only via `tools`) |

A scheduled report is an e-mail report that Matomo renders and sends on a schedule. Its `idReport` counts for the
whole instance, and a report belongs to one website. Tools that create, resume, or send a report do not exist,
because sending is a permanent path of data to recipients outside Matomo. Qatlas reads, renders as text, pauses, and
deletes.

- `matomo.scheduled-reports.list` returns the reports Matomo shows the token's user: all of them for a Super User,
  otherwise the user's own. `idSite`, `period`, `idReport`, `ifSuperUserReturnOnlySuperUserReports`, and `idSegment`
  narrow the list as in Matomo. Matomo has no paging for it, so Qatlas cuts the batches and continues with
  `next_cursor`. A report of a website outside the connection's targets is left out. The recipients (additional
  e-mail addresses, phone numbers) and the creator's login are personal data, so the tool belongs to the profile
  `live`, not to `read`. Descriptions and recipients are untrusted provider data.
- `matomo.scheduled-reports.generate` renders one report for a date and returns it as text. Only the formats `html`,
  `csv`, and `tsv` are offered; binary formats such as `pdf` are refused. Qatlas always asks Matomo to return the
  text to the caller, never to stream it or to write a file on the server, and the tool has no argument for the
  output mode or for transport parameters. Nothing is sent to a recipient. The text is cut at a limit and is
  untrusted. An `html` report with graphs can exceed the size Qatlas reads from Matomo and is then refused; `csv`
  and `tsv` are compact. The data period is `period` or else the period the report stores, and a date range needs
  the period `range`. It is in the profile `live` because its report ID comes from the list.
- `matomo.scheduled-reports.pause` stops the sending of one report by setting only its schedule to never; the tool
  has no argument for recipients or any other setting, and resuming is possible only in Matomo. Matomo's update call
  replaces the whole report, so Qatlas reads the report once and sends every other value back as read. Before it
  writes, it checks that Matomo would store exactly the values it read, and it refuses without a write when it
  cannot show that: for a report that is not an e-mail report, a member or parameter Qatlas does not know, an
  e-mail parameter missing or of another form than Matomo writes (the three flags are booleans, the display format
  is text), an empty or unnormalised recipient list, or an evolution, hour, segment, or data period of another form.
  Such a report is paused in Matomo. A report whose data period was never set explicitly keeps the period Matomo
  derived from its former schedule. A report that is already paused is not written and answers `alreadyPaused`.
  Matomo may also store a language for the token's user when it has none. The tool is offered by a connection that
  allows the effect `update`, not only via `tools`.
- `matomo.scheduled-reports.delete` deletes one report so that it is no longer sent. Matomo marks the report as
  deleted and Qatlas cannot restore it. Only the creator or a Super User may delete it. The tool is offered only
  where the `tools` list of a connection names it and is in no profile.

Matomo asks for no password confirmation on any of them.

Because the report ID is instance-wide, `generate`, `pause`, and `delete` read the report once before they act and
refuse a report of a website outside the connection's targets as not found, without telling whether it exists. A
report ID that Matomo does not hold is also not found, and so is a report that belongs to another website than the
`idSite` of `pause`.

The pause and the deletion need the confirmation of the caller and are sent exactly once. After a result that is
unclear (no answer in time, a reset connection, an answer Qatlas cannot read) the error says the change may have been
applied and is never repeated; read the report with `matomo.scheduled-reports.list` and its `idReport` before trying again.
