---
description: >
  Describes the Matomo A/B test tools: listing and reading experiments, the experiment options, the code templates, creating, replacing, and deleting experiments, the binding of an experiment to the connection's targets, how missing rights affect the options tool, the plugin requirement, and the limits of what the AbTesting reference proves.
type: knowledge
edit: shared
created: 2026-11-02
updated: 2026-11-02
---

# Matomo A/B tests

The tools for A/B test experiments of the [Matomo provider](README.md). They need the premium plugin
AbTesting; an instance without it refuses all of them as an unsupported capability, after one plugin check and
without further requests. The tools are in no [setup profile](README.md#setup-profiles); the delete tool is
offered only where a connection's `tools` list names it. Reports of the plugin are read with `matomo.reports.get`
like any other report. Starting, finishing, and archiving experiments is not offered.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.experiments.list` | reads the experiments of a website with their targets, success metrics, and variations, in batches |
| `matomo.experiments.get` | reads one experiment of a website |
| `matomo.experiment-options.list` | reads the statuses and success metrics of a website and the target attributes |
| `matomo.experiment-templates.get` | reads the JavaScript snippet of an experiment, or the script tag of the tracker, as text |
| `matomo.experiments.add` | creates an experiment; it is not started |
| `matomo.experiments.update` | replaces the whole definition of an experiment |
| `matomo.experiments.delete` | deletes an experiment |

- `matomo.experiments.list` lists all experiments. `active` lists only the active ones, and `statuses` (1 to 10
  different statuses) only those of these statuses. The two cannot be combined, so each call asks one method.
- `matomo.experiment-templates.get` takes `kind`: `experiment` (with `idExperiment`) or `include`. The method of
  `include` names no website and no experiment, so the tool takes neither argument for it and is offered only
  where the connection binds a website.
- Answers of the option methods and of the list methods have no documented example; the options are returned as
  Matomo gives them, shortened.

- The three change tools need `confirm`, send one request, and never repeat it: after a timeout, a reset
  connection, a server error, or an unreadable answer they say the outcome is unclear and name the read that settles
  it. `add` is not idempotent, so a repeated call creates a second experiment.
- `matomo.experiments.update` replaces the definition as a whole, as the reference requires all but five of its
  parameters. It takes the complete definition and fills nothing in from the stored experiment: a value that is not
  required and not given is not sent, so Matomo applies its own default and may reset what the experiment had.
  `matomo.experiments.add` takes only the seven parameters the reference lists for creating; confidence, effect size,
  share of participants, excluded targets, dates, and parameter forwarding are set by `update`.

Names, hypotheses, target rules, variations, and snippets are untrusted provider data. They are shortened, an address
is returned as text and never fetched, and a snippet is returned as text and never run or interpreted. An
experiment shows at most 100 targets, success metrics, and variations per kind.

## Targets

For `update` and `delete`, one read of the experiment proves it belongs to the website before the change request is
sent; an experiment of another website, or one without a website, ends as `not-found` and nothing is changed.
`idSite` must be inside the targets ([Scope](README.md#scope)) and is checked before any secret is read; a website
outside them is refused without naming it. An experiment ID is not tied to a website in the request, so the answer
must carry the website it belongs to: an experiment of another website, or an answer without a website, is not
shown and ends as `not-found` without naming another website. In a list, such rows are dropped. For `kind
experiment` the experiment is read and proven for the website before the snippet is asked for, because the
reference does not say that the snippet method checks it; a foreign experiment never reaches the snippet method.

## Rights of the options tool

The rights are not checked in advance. When Matomo refuses an option for the right of the token, the option is
left out of the result and named in `unavailable` with the reason `permission`; the result is never silently
shortened. When Matomo refuses every option, the call fails with a permission error. Any other failure of one
option ends the whole call.

## Limits

- The reference types `variations`, `includedTargets`, `excludedTargets`, and `successMetrics` as lists of strings and
  states no member names. The tools send variations as `variations[N][name|percentage|redirect_url]`, targets as
  `includedTargets[N][attribute|type|value|inverted]`, and metrics as `successMetrics[N]`, after the members the read
  methods report. This format is not verified against an instance.
- The answers of the change methods have no documented example. `addExperiment` is taken to return the new ID and the
  other two the generic success form; any other answer ends as unclear. The rights of the change methods (a write
  right on the website) are expected, not proven, and a refusal by Matomo ends as a permission error.
- The bounds of `mdeRelative` and `percentageParticipants` (1 to 100), of the texts, and of the metric names are
  Qatlas's own and narrower than the reference. Dates are accepted only as `YYYY-MM-DD HH:MM:SS`; the reference's
  `false` for no date is expressed by leaving the argument out.
- The reference states no right for any method. The public demo answered an anonymous token with a write right
  required on the website for the list methods, the option methods, and the snippet methods, and for any
  website for the target attributes and the tracker tag, while `getExperiment` was answered with a view right.
  The Marketplace changelog (3.2.3) supports a view right only indirectly; the rights are not proven per method.
- The method signatures come from the 6.x reference. The Marketplace lists AbTesting 5.6.2 for Matomo 5 and 6.0.4
  for Matomo 6; the signatures, including those of the change methods, are not verified for Matomo 5. No version is checked per method, and an instance that
  lacks one ends as an unsupported capability.
- The reference gives only `getExperiment` an example. The shape of the list rows is taken from it, and the shape of the
  option and snippet answers (a string, also as `{"value":...}`) and the array form `statuses[]` of the request are
  assumptions. An experiment without a website is treated as not belonging to it.
- The limits of 100 entries and of 16384 bytes of snippet text are Qatlas's own.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; an unknown experiment ends as `not-found` by the common message or an answer of `false`.
