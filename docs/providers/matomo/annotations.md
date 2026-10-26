---
description: >
  Describes the Matomo annotation tools: listing, reading, counting, adding, updating, and deleting the notes on a website's days, their limits and unclear outcomes.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo annotations

The annotation tools of the [Matomo provider](README.md). The profiles are described under
[Setup profiles](README.md#setup-profiles). The rights and the plugin are listed in [rights per tool group](setup.md#rights-per-tool-group).

## Tools

| Tool | Does |
| --- | --- |
| `matomo.annotations.list` | reads the annotations of a website in a date range, in batches |
| `matomo.annotations.get` | reads one annotation of a website |
| `matomo.annotations.add` | adds an annotation to a day (effect `create`) |
| `matomo.annotations.update` | changes the date, note, or star of an annotation (effect `update`) |
| `matomo.annotations.count` | counts the annotations of a website per period, with starred counts |
| `matomo.annotations.delete` | deletes one annotation for good (effect `delete`, only via `tools`) |
| `matomo.annotations.delete-all` | deletes every annotation of a website; needs the `instance` target (effect `delete`, only via `tools`) |

Annotations are per website; `idNote` counts per website, and every tool works with `idSite` and `idNote` together.
Matomo checks that pair itself and answers an ID the website does not have as an error, so no tool reads first.

- `matomo.annotations.list` returns the annotations of one website in a date range, ordered by date and ID.
  `date`, `period`, and `lastN` shape the range as in Matomo; without `date` every annotation of the website is
  returned. Matomo has no paging for it, so Qatlas cuts the batches and continues with `next_cursor`. One website
  per call: a list of website IDs is not accepted, and rows of another website are dropped.
- `matomo.annotations.get` returns one annotation.
- `matomo.annotations.add` attaches a note to a day (`YYYY-MM-DD`, `today`, or `yesterday`), optionally starred.
  It is not idempotent, a repeated call creates a second annotation.
- `matomo.annotations.update` changes the date, note, or star; a value not given stays as it is, and at least one
  is required.
- `matomo.annotations.count` counts the annotations of one website per period of `date`, `period`, and `lastN`
  (both `date` and `period` are required, as in Matomo; `range` counts per day), with the number of starred ones.
  `getAnnotationText` adds the note for a period with exactly one annotation. One website per call. It is part of
  the `read` profile.
- `matomo.annotations.delete` deletes one annotation for good. Matomo pairs `idSite` and `idNote` itself and answers
  an ID the website does not have as an error, so no read comes first.
- `matomo.annotations.delete-all` deletes every annotation of a website for good, whoever wrote it. The connection must bind the target `instance`.

Both delete tools are offered only where the `tools` list of a connection names them and are in no profile.

The fields `idNote` and `id` of Matomo carry the same value, so only `idNote` is returned. `user` is the login of
the author and `canEditOrDelete` tells whether the token's user may change the annotation; both are returned.
Note text and login are untrusted provider data; Matomo returns the note HTML-escaped. A note holds 1 to 255
characters; Matomo would cut a longer one silently, so Qatlas refuses it before sending.

Every change needs the confirmation of the caller and is sent exactly once. After a result that is unclear (no
answer in time, a reset connection, an answer Qatlas cannot read) the error says the change may have been applied
and is never repeated; read the annotation with `matomo.annotations.list` or `matomo.annotations.get` before
trying again.
