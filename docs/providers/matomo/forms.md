---
description: >
  Describes the Matomo form tools: listing and reading the forms of a website, the real-time counters and most popular forms, the form options, creating, changing, archiving, and deleting forms, the binding of a form to the connection's targets, how missing rights affect the options tool, the plugin requirement, and the limits of what the FormAnalytics reference proves.
type: knowledge
edit: shared
created: 2026-11-02
updated: 2026-11-02
---

# Matomo forms

The tools for forms of the [Matomo provider](README.md). They need the premium plugin FormAnalytics; an
instance without it refuses all of them as an unsupported capability, after one plugin check and without further
requests. The tools are in no [setup profile](README.md#setup-profiles). Reports of the plugin, and the page URLs
of its forms, are read with `matomo.reports.get` like any other report. The tools that change forms are offered to
a connection by its `effects`, except the deleting one, which is offered only via `tools`.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.forms.list` | reads the forms of a website with their rules and fields, in batches |
| `matomo.forms.get` | reads one form of a website |
| `matomo.forms.realtime` | reads the counters of all forms, or the currently most popular forms, of the last minutes |
| `matomo.form-options.list` | reads the values forms are configured with, the auto-creation settings, and the goals of a website |
| `matomo.forms.add` | creates a form (effect `create`) |
| `matomo.forms.update` | replaces the configuration of a form, or sets the display names of its fields (effect `update`) |
| `matomo.forms.archive` | archives a form (effect `update`) |
| `matomo.forms.delete` | deletes a form (effect `delete`, only via `tools`) |

- `matomo.forms.list` lists all forms. With `statuses` (1 to 10 different statuses) it lists only the forms of
  these statuses; the values come from `matomo.form-options.list`.
- `matomo.forms.realtime` takes `kind`, which chooses the method: `counters` for the counters of all forms, or
  `popular-forms` for the currently most popular forms. `lastMinutes` is required. The optional `segment` filters
  the visits. `limit` is the number of forms for `popular-forms` only; it is sent as the row limit, never as
  all rows.
- `matomo.form-options.list` reads the statuses, the building blocks of form rules and page rules, and the
  conversion rule options, which belong to no website, and the auto-creation settings and goals of the chosen
  website. Answers the reference gives no example for are returned as Matomo gives them, shortened.

- The changing tools ask for `confirm` and send exactly one request, never repeated by Qatlas: after a timeout, a
  reset connection, a server error, or an unreadable answer, the result is unclear and the form is read with
  `matomo.forms.get` before the call is repeated. `matomo.forms.add` is not idempotent, so a repeated call creates
  a second form.
- `matomo.forms.add` and `matomo.forms.update` pass only the settings given, so Matomo applies its defaults to the
  rest. `matomo.forms.update` with `name` replaces the whole configuration, and a setting left out may reset a
  value the form had; an empty rule list cannot be sent, so rules can be replaced but not cleared. With `fields`
  instead of `name` and the other settings it sets the display names of known fields, matched by field name, in
  one request. The two ways are exclusive, because each is one Matomo method and one request.
- The rules of `matchFormRules`, `matchPageRules`, and `conversionRules` hold `attribute`, `pattern`, and `value`;
  attribute and pattern are lowercase words from `matomo.form-options.list`, and at most 100 rules are accepted per
  list. Addresses in rules are sent as text and never fetched.
- `matomo.forms.update`, `matomo.forms.archive`, and `matomo.forms.delete` first read the form with the same call
  as `matomo.forms.get`; the change is sent only when the form belongs to the website. Archiving and deleting
  cannot be undone through Qatlas, and the reference does not say what they do to the recorded data.

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

- A view right on the website is expected for each reading method and a write right for each changing method; the
  reference does not state it for any of them.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5; no version is
  checked per method, and an instance that lacks one ends as an unsupported capability.
- The reference gives `getForm` and the five option methods no JSON example, and describes the answers of
  `getAutoCreationSettings` and `getAllGoals` only in words. The shape of a form is taken from the examples of
  `getForms`; the shape of the answer of `getAllGoals` as a map of goal IDs to names, and the array form
  `statuses[]` of the request, are assumptions. The member `idsite` of a row of the most popular forms is an
  assumption too.
- The reference gives the changing methods no JSON example. The ID `addForm` returns, and the generic success
  answer `{"result":"success","message":"ok"}` of the other four, are assumptions; any other answer is reported as
  unclear. The rule format (members `attribute`, `pattern`, `value`, sent as `matchFormRules[N][member]`) and the
  field format (`fields[N][name]`, `fields[N][displayName]`) come only from the reference's examples, and the
  signatures come from the Matomo 6 reference and are not verified against Matomo 5.
- The text limits (255 characters for names and display names, 2048 for descriptions and rule values) are
  Qatlas's own.
- The look-back limit of 2880 minutes, the row limit of 100 for the most popular forms (Matomo's default is 5), and
  the limits of 100 rules, 200 fields, and 100 goals are Qatlas's own. `getCounters` and
  `getCurrentMostPopularForms` accept the segment as the reports do; its shape is checked, not its fields.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; an unknown form ends as `not-found` by the common message or an answer of `false`.
