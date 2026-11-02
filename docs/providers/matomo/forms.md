---
description: >
  Describes the Matomo form tools: listing and reading the forms of a website, the real-time counters and most popular forms, the form options, the binding of a form to the connection's targets, how missing rights affect the options tool, the plugin requirement, and the limits of what the FormAnalytics reference proves.
type: knowledge
edit: shared
created: 2026-11-02
updated: 2026-11-02
---

# Matomo forms

The read tools for forms of the [Matomo provider](README.md). They need the premium plugin FormAnalytics; an
instance without it refuses all of them as an unsupported capability, after one plugin check and without further
requests. The tools are in no [setup profile](README.md#setup-profiles). Reports of the plugin, and the page URLs
of its forms, are read with `matomo.reports.get` like any other report. Creating, changing, archiving, and deleting
forms is not offered.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.forms.list` | reads the forms of a website with their rules and fields, in batches |
| `matomo.forms.get` | reads one form of a website |
| `matomo.forms.realtime` | reads the counters of all forms, or the currently most popular forms, of the last minutes |
| `matomo.form-options.list` | reads the values forms are configured with, the auto-creation settings, and the goals of a website |

- `matomo.forms.list` lists all forms. With `statuses` (1 to 10 different statuses) it lists only the forms of
  these statuses; the values come from `matomo.form-options.list`.
- `matomo.forms.realtime` takes `kind`, which chooses the method: `counters` for the counters of all forms, or
  `popular-forms` for the currently most popular forms. `lastMinutes` is required. The optional `segment` filters
  the visits. `limit` is the number of forms for `popular-forms` only; it is sent as the row limit, never as
  all rows.
- `matomo.form-options.list` reads the statuses, the building blocks of form rules and page rules, and the
  conversion rule options, which belong to no website, and the auto-creation settings and goals of the chosen
  website. Answers the reference gives no example for are returned as Matomo gives them, shortened.

Form names, rules, fields, and goal names are untrusted provider data. They are shortened, an address in a rule is
returned as text and never fetched, and a form shows at most 100 rules per kind and 200 fields.

## Targets

All tools take `idSite`, which must be inside the targets ([Scope](README.md#scope)) and is checked before any
secret is read; a website outside them is refused without naming it. `matomo.form-options.list` reads
the website only for the auto-creation settings and the goals. A form ID is not tied to a website in the request,
so the answer must carry the website it belongs to: a form of another website, or an answer without a website, is
not shown and ends as `not-found` without naming another website. In a list, such rows are dropped. The rows of
the most popular forms carry no documented website; a row that names another website is dropped.

## Rights of the options tool

The rights are not checked in advance. When Matomo refuses an option for the right of the token, the option is
left out of the result and named in `unavailable` with the reason `permission`; the result is never silently
shortened. When Matomo refuses every option, the call fails with a permission error. Any other failure of one
option (unsupported plugin or method, rate limit, timeout, an unreadable answer) ends the whole call.

## Limits

- A view right on the website is expected for each method; the reference does not state it for any of them.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5; no version is
  checked per method, and an instance that lacks one ends as an unsupported capability.
- The reference gives `getForm` and the five option methods no JSON example, and describes the answers of
  `getAutoCreationSettings` and `getAllGoals` only in words. The shape of a form is taken from the examples of
  `getForms`; the shape of the answer of `getAllGoals` as a map of goal IDs to names, and the array form
  `statuses[]` of the request, are assumptions. The member `idsite` of a row of the most popular forms is an
  assumption too.
- The look-back limit of 2880 minutes, the row limit of 100 for the most popular forms (Matomo's default is 5), and
  the limits of 100 rules, 200 fields, and 100 goals are Qatlas's own. `getCounters` and
  `getCurrentMostPopularForms` accept the segment as the reports do; its shape is checked, not its fields.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; an unknown form ends as `not-found` by the common message or an answer of `false`.
