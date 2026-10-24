---
description: >
  Describes the Matomo annotation tools: listing, reading, adding, and updating the notes on a website's days, their rights, limits, and unclear outcomes.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Matomo annotations

The annotation tools of the [Matomo provider](README.md). The profiles are described under
[Setup profiles](README.md#setup-profiles). All four tools need the Annotations plugin.

Annotations are per website; `idNote` counts per website, and every tool works with `idSite` and `idNote` together.
Matomo checks that pair itself and answers an ID the website does not have as an error, so no tool reads first.
Deleting and counting annotations are not offered.

- `matomo.annotations.list` returns the annotations of one website in a date range, ordered by date and ID.
  `date`, `period`, and `lastN` shape the range as in Matomo; without `date` every annotation of the website is
  returned. Matomo has no paging for it, so Qatlas cuts the batches and continues with `next_cursor`. One website
  per call: a list of website IDs is not accepted, and rows of another website are dropped.
- `matomo.annotations.get` returns one annotation.
- `matomo.annotations.add` attaches a note to a day (`YYYY-MM-DD`, `today`, or `yesterday`), optionally starred.
  Matomo 5 needs a view right on the website for a named user, Matomo 6 needs a write right. It is not idempotent,
  a repeated call creates a second annotation.
- `matomo.annotations.update` changes the date, note, or star; a value not given stays as it is, and at least one
  is required. It needs a write right on the website, or the user is the author of the annotation.

The fields `idNote` and `id` of Matomo carry the same value, so only `idNote` is returned. `user` is the login of
the author and `canEditOrDelete` tells whether the token's user may change the annotation; both are returned.
Note text and login are untrusted provider data; Matomo returns the note HTML-escaped. A note holds 1 to 255
characters; Matomo would cut a longer one silently, so Qatlas refuses it before sending.

Every change needs the confirmation of the caller and is sent exactly once. After a result that is unclear (no
answer in time, a reset connection, an answer Qatlas cannot read) the error says the change may have been applied
and is never repeated; read the annotation with `matomo.annotations.list` or `matomo.annotations.get` before
trying again.
