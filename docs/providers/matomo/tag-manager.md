---
description: >
  Describes the Matomo Tag Manager tools: reading the containers of a website, their versions and their export, the tags and triggers of a version, the embed code, and the catalog of contexts, environments, and templates, creating, renaming, and deleting container versions, and adding, updating, and deleting triggers of the draft, with their rights and limits.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Matomo Tag Manager

The Tag Manager tools of the [Matomo provider](README.md). They need the plugin TagManager; an instance without it
reports the tools as unsupported. Reading needs a view right on the website, changing a write right, and the instance
setting that restricts Tag Manager access to a higher role applies on top. Every website tool takes `idSite`, checked against the connection's targets before any
secret or request. All tools are in no profile and are offered only via `tools`: container names, release logins, and
the paths of non-live releases are data of the website's owners, and the change tools alter what a container does.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.tag-manager-containers.list` | reads the containers of a website with their draft, versions, and releases |
| `matomo.tag-manager-containers.get` | reads one container of a website |
| `matomo.tag-manager-containers.get-embed-code` | reads the embed snippet or the installation steps of a container release |
| `matomo.tag-manager-versions.list` | reads the saved versions of a container with their releases |
| `matomo.tag-manager-versions.get` | reads one version of a container, the draft included |
| `matomo.tag-manager-versions.export` | reads the export of one version: container, version, tags, triggers, and variables |
| `matomo.tag-manager-versions.create` | saves the draft, or a copy of a saved version, as a new version |
| `matomo.tag-manager-versions.update` | renames a version and replaces its description |
| `matomo.tag-manager-versions.delete` | deletes a saved version |
| `matomo.tag-manager-tags.list` | reads the tags of a container version, the draft included, with parameters, trigger IDs, and fire settings |
| `matomo.tag-manager-tags.get` | reads one tag of a container version |
| `matomo.tag-manager-triggers.list` | reads the triggers of a container version, the draft included, with parameters and conditions |
| `matomo.tag-manager-triggers.get` | reads one trigger of a container version |
| `matomo.tag-manager-triggers.add` | adds a trigger to the draft |
| `matomo.tag-manager-triggers.update` | replaces name, parameters, conditions, and description of a trigger of the draft |
| `matomo.tag-manager-triggers.delete` | deletes a trigger from the draft |
| `matomo.tag-manager-catalog.list` | reads contexts, environments, fire limits, comparisons, and the tag, trigger, and variable templates |

- A container belongs to one website: the container ID is looked up with `idSite`, so the ID of another website is
  not found, and a row that names another website is dropped or refused.
- A container lists at most 50 versions and 50 releases; `versions_truncated` and `releases_truncated` say when there
  are more. The draft is the one version that changes, with revision 0; a release ties a version to an environment.
- `matomo.tag-manager-containers.get-embed-code` returns text for a person to review. Snippets, steps, and addresses
  are untrusted: they are cut at a limit, never run, and never fetched. The snippet exists for the web context only;
  a mobile container answers with an empty snippet, and its location is in the description of the step. The
  environment is any ID of `matomo.tag-manager-catalog.list` with `kind` `environments`.
- A version is bound by the website and the container together: Matomo looks it up with `idSite`, `idContainer`, and
  the version ID, so an ID of another container or website is not found, and a row that names another one is dropped
  or refused. The list leaves out the draft; `matomo.tag-manager-versions.get` reads it by ID, and
  `matomo.tag-manager-versions.export` exports it when no version is given.
- The export is data for a person to review. Custom HTML, scripts, and addresses in the parameters of tags,
  triggers, and variables are untrusted text: they are cut at limits, never run, and never fetched; `truncated` says
  when anything was cut. The presentation metadata of the templates is not part of it.
- Tags and triggers are bound by the website, the container, and the version together: Matomo looks them up with
  `idSite`, `idContainer`, `idContainerVersion`, and the tag or trigger ID, so an ID of another version, container,
  or website is not found. A listed row that names another website or version is dropped, a single one is refused.
  The version is required; the ID of the draft is in `matomo.tag-manager-containers.get`. Parameters, conditions,
  and custom HTML are untrusted text with the same limits as the export; `truncated` says when anything was cut.
  The lists page in batches, with a cursor bound to the website, container, and version.
  The template metadata Matomo adds to a row is not returned.
- `matomo.tag-manager-catalog.list` calls one fixed method per `kind`. The template kinds need a context from `kind`
  `contexts` and return the templates flattened from their categories with their parameters (cut at 100 per template
  and 100 offered values per parameter). `publishable-environments` needs `idSite` and leaves out `live` for a token
  without the right to publish there. The presentation hints `fullWidth`, `component`, and the compliance metadata of
  a parameter are not returned.

- A version is a snapshot of the draft. `matomo.tag-manager-versions.create` copies the draft (or the version given
  as `idContainerVersion`) into a new revision; the draft keeps its content and nothing is released to an
  environment. The tools change the draft's name and the saved versions only; they never change tags, triggers, or
  variables and never publish. Matomo refuses a name that another version of the container already uses; the refusal is reported as
  such and nothing changed.
- `matomo.tag-manager-versions.update` replaces name and description as given, so `description` is required and an
  empty text clears it. The draft can be renamed, too.
- `matomo.tag-manager-versions.delete` is idempotent in effect and cannot be undone. Matomo refuses the draft and a
  version that is released to an environment, so no live version is removed; the refusal is reported as such and
  nothing changed. It is offered only via `tools`.
- The change tools send exactly one request after confirmation and never repeat it. After a timeout, a reset, a
  server error, or an answer that cannot be read, the result is unknown: read the versions (or, for the trigger
  tools, the triggers with `matomo.tag-manager-triggers.list`) before trying again. A container and a version are bound by Matomo to `idSite`
  (and `idContainer`) before it changes anything, so no prior read is made and an ID of another website is not found.
- Names and descriptions are untrusted text. A name has 1 to 50 characters (Matomo's API limit), a description up to
  1000.

- The trigger tools change the draft only and take no `idContainerVersion`: they read the container once, take the
  draft from it, and send one request into that version, so a saved version is never altered although Matomo itself
  would accept it. Without a usable draft the tool refuses before it writes. Triggers collect nothing but decide when
  tags fire; a trigger is bound to the website, container, and draft together.
- `matomo.tag-manager-triggers.add` takes `type`, `name`, and optionally `parameters`, `conditions`, and
  `description`; the type cannot change later. `matomo.tag-manager-triggers.update` replaces everything as given:
  `parameters`, `conditions`, and `description` are required, an empty object or list clears them, and Matomo
  resets the parameters that are left out to their defaults. Matomo accepts an update or deletion of an ID the draft
  does not hold without changing anything.
- `parameters` and `conditions` are structured and capped: a parameter is a text, a number, a flag, or a list of
  such values or of rows of them, with at most 100 parameters, 100 list members, 50 cells per row, 50 conditions,
  16384 bytes per text, and 262144 bytes of text in all. Matomo keeps only the parameters its trigger type defines
  and checks the rest: the variable a condition names must exist in the draft or be pre-configured.
- Matomo refuses a name another trigger of the draft uses, a trigger that a tag still uses when it is deleted, an
  unknown trigger type, and a condition with a missing value, an unknown comparison, or an unknown variable; each is
  reported as such and nothing changed. A custom template needs the right to use custom templates.
- `matomo.tag-manager-triggers.delete` cannot be undone and is offered only via `tools`.

Container changes, importing a version, tags, variables, and publishing a release are not part of these tools.
