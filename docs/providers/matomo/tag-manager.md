---
description: >
  Describes the Matomo Tag Manager tools: reading the containers of a website, their versions and their export, the tags, triggers, and variables of a version and where they are used, the embed code, and the catalog of contexts, environments, and templates, creating, renaming, deleting, and publishing container versions, adding, updating, and deleting triggers of the draft, adding, updating, pausing, resuming, and deleting tags of the draft, adding, updating, and deleting variables of the draft, and creating, updating, and deleting containers, with their rights and limits.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Matomo Tag Manager

The Tag Manager tools of the [Matomo provider](README.md). They need the plugin TagManager; an instance without it
reports the tools as unsupported. Reading needs a view right on the website, changing a write right (publishing more,
see [Publishing](#publishing)), and the instance setting that restricts Tag Manager access to a higher role applies on top. Every website tool takes `idSite`, checked against the connection's targets before any
secret or request. All tools are in no profile and are offered only via `tools`: container names, release logins, and
the paths of non-live releases are data of the website's owners, the change tools alter what a container does, and
publishing changes the script that visitors run.

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
| `matomo.tag-manager-variables.list` | reads the configured variables of a container version, the draft included, with parameters, default value, and lookup table |
| `matomo.tag-manager-variables.get` | reads one configured variable of a container version |
| `matomo.tag-manager-variables.list-available` | reads the configured and the preconfigured variables a container version offers |
| `matomo.tag-manager-references.list` | reads where a trigger or a variable of a container version is used, chosen by `kind` |
| `matomo.tag-manager-triggers.add` | adds a trigger to the draft |
| `matomo.tag-manager-triggers.update` | replaces name, parameters, conditions, and description of a trigger of the draft |
| `matomo.tag-manager-triggers.delete` | deletes a trigger from the draft |
| `matomo.tag-manager-tags.delete` | deletes a tag from the draft |
| `matomo.tag-manager-tags.pause` | pauses a tag of the draft |
| `matomo.tag-manager-tags.add` | adds a tag of the type `Matomo` or `ThemeColor` to the draft |
| `matomo.tag-manager-tags.update` | replaces name, parameters, trigger IDs, fire settings, and description of a tag of the draft |
| `matomo.tag-manager-tags.resume` | activates a paused tag of the draft again |
| `matomo.tag-manager-variables.add` | adds a variable to the draft |
| `matomo.tag-manager-variables.update` | replaces name, parameters, default value, lookup table, and description of a variable of the draft |
| `matomo.tag-manager-variables.delete` | deletes a variable from the draft |
| `matomo.tag-manager-containers.create-default` | creates the default container of a website |
| `matomo.tag-manager-containers.add` | creates a container with a context, name, and settings |
| `matomo.tag-manager-containers.update` | replaces name, description, and settings of a container |
| `matomo.tag-manager-containers.delete` | deletes a container with everything in it |
| `matomo.tag-manager-versions.publish` | checks a saved version and releases it to an environment |
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
- Tags, triggers, and variables are bound by the website, the container, and the version together: Matomo looks them
  up with `idSite`, `idContainer`, `idContainerVersion`, and the tag, trigger, or variable ID, so an ID of another version, container,
  or website is not found. A listed row that names another website or version is dropped, a single one is refused.
  The version is required; the ID of the draft is in `matomo.tag-manager-containers.get`. Parameters, conditions,
  and custom HTML are untrusted text with the same limits as the export; `truncated` says when anything was cut.
  The lists page in batches, with a cursor bound to the website, container, and version.
  The template metadata Matomo adds to a row is not returned.
- Variable code (such as the function of a `CustomJsFunction` variable), default values, and lookup tables are
  untrusted text with the same limits. `matomo.tag-manager-variables.list` leaves out the preconfigured variables;
  `matomo.tag-manager-variables.list-available` flattens both kinds from their categories and pages the same way.
- `matomo.tag-manager-references.list` calls one fixed method per `kind`: `trigger` (with `idTrigger`) lists the tags
  that fire or block on the trigger, `variable` (with `idVariable`) lists the tags, triggers, and variables that
  use it. The other ID is refused. Matomo reports an ID the version does not hold as having no references, so an
  empty list does not prove the object exists; the cursor is bound to the kind and the ID.
- `matomo.tag-manager-catalog.list` calls one fixed method per `kind`. The template kinds need a context from `kind`
  `contexts` and return the templates flattened from their categories with their parameters (cut at 100 per template
  and 100 offered values per parameter). `publishable-environments` needs `idSite` and leaves out `live` for a token
  without the right to publish there. The presentation hints `fullWidth`, `component`, and the compliance metadata of
  a parameter are not returned.

- A version is a snapshot of the draft. `matomo.tag-manager-versions.create` copies the draft (or the version given
  as `idContainerVersion`) into a new revision; the draft keeps its content and nothing is released to an
  environment. These tools change the draft's name and the saved versions only; they never change tags, triggers, or
  variables and never publish. Matomo refuses a name that another version of the container already uses; the refusal is reported as
  such and nothing changed.
- `matomo.tag-manager-versions.update` replaces name and description as given, so `description` is required and an
  empty text clears it. The draft can be renamed, too.
- `matomo.tag-manager-versions.delete` is idempotent in effect and cannot be undone. Matomo refuses the draft and a
  version that is released to an environment, so no live version is removed; the refusal is reported as such and
  nothing changed. It is offered only via `tools`.
- The change tools send exactly one request after confirmation and never repeat it; `publish` reads the export once
  before and once after it (see [Publishing](#publishing)). After a timeout, a reset, a server error, or an answer that cannot be read, the result is unknown: read the versions (or, for the trigger
  and tag tools, the triggers or tags with `matomo.tag-manager-triggers.list` or `matomo.tag-manager-tags.list`) before trying again. A container and a version are bound by Matomo to `idSite`
  (and `idContainer`) before it changes anything, so an ID of another website is not found.
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
- The tag tools bind to the draft the same way as the trigger tools and publish nothing. `matomo.tag-manager-tags.delete`
  and `matomo.tag-manager-tags.pause` take only `idSite`, `idContainer`, and `idTag`. Pausing takes effect live after a
  release. Deleting cannot be undone and removes a data path; it is offered only via `tools`. Both need the right to
  use custom templates for a tag of a custom template; without it the refusal is a permission error. After an unclear
  result read the tags with `matomo.tag-manager-tags.list` before trying again.
- `matomo.tag-manager-tags.add`, `matomo.tag-manager-tags.update`, and `matomo.tag-manager-tags.resume` accept only the
  tag types `Matomo` and `ThemeColor`, a fixed list that `matomo.tag-manager-catalog.list` does not extend: every
  other type can send data to a third party. `add` refuses another type before any request; `update` and `resume`
  read the stored tag once and refuse one of another type before they change anything. `update` replaces everything
  as given and cannot change the type; `add` takes `status` (`active` or `paused`). `parameters` are capped like
  those of triggers; Matomo keeps only the parameters the type defines.
- A `Matomo` tag sends to the address of the variable its `matomoConfig` refers to. The reference must be exactly one
  `{{Name}}` that names a variable of the draft of the type `MatomoConfiguration`, whose `matomoUrl` and `idSite` are
  written out (no reference) and equal this connection's base URL (scheme, host, port, and path, ignoring a trailing
  slash) and the selected website. The tool reads the variables of the draft once for it; otherwise it refuses
  without a request to change anything and without naming the other value. `resume` applies the rule to the stored
  `matomoConfig`. The variable tools hold the variable itself to the same rule.
- The container tools create a container with its draft and change its name, description, and settings; they save no
  version and publish nothing. `matomo.tag-manager-containers.add` takes a `context` that cannot change later;
  `matomo.tag-manager-containers.update` replaces everything as given, so `description` and the three settings are
  required and an empty `description` clears it. `ignoreGtmDataLayer` and `activelySyncGtmDataLayer` act only after a
  later publication of a version. Updating and deleting need the right to publish in addition to the write right.
- `matomo.tag-manager-containers.update` and `matomo.tag-manager-containers.delete` read the container once before
  the request, so an ID the website does not hold is refused as not found before anything changes. A container name has
  1 to 50 characters, a description up to 1000.
- `matomo.tag-manager-containers.delete` removes all versions, releases, tags, triggers, and variables and cannot be
  undone. When the container was live, its container script disappears from the website and its tags stop running. It
  is offered only via `tools`. After an unclear result of a container tool, read the containers with
  `matomo.tag-manager-containers.list`. `matomo.tag-manager-containers.create-default` returns `idContainer` only when
  Matomo reports it.
- The variable tools bind to the draft like the trigger tools and publish nothing. `add` takes `type`, `name`, and
  optionally `parameters`, `defaultValue`, `lookupTable`, and `description`; `update` replaces everything as given
  (all of these but the type are required; an empty object, text, or list clears) and cannot change the type;
  `delete` takes only the IDs, cannot be undone, and is offered only via `tools`. `parameters` are capped like those
  of triggers; a lookup row is `matchValue`, `comparison`, and `outValue`, at most 100 rows. Matomo refuses a name
  another variable of the draft uses; the refusal is reported as such and nothing changed. After an unclear result
  read the variables with `matomo.tag-manager-variables.list`.
- The types `CustomJsFunction` and `CustomRequestProcessing` run custom code in the visitors' browsers; the tools
  refuse them, a fixed list. `add` refuses before any request; `update` reads the stored variable once and refuses one
  of those types before it changes anything.
- A variable of the type `MatomoConfiguration` decides where `Matomo` tags send their data, so `add` and `update`
  accept it only under the rule of the `Matomo` tags above: `matomoUrl` and `idSite` written out and equal to this
  connection's base URL and the selected website. In addition `setApiUrl` and `customRequestProcessing` must be
  empty, and `trackingEndpoint`, `trackingEndpointCustom`, `jsEndpoint`, and `jsEndpointCustom` must be plain paths
  (no scheme, host, reference, or `..`). `update` applies this by the stored type; the refusal names no value.

## Publishing

`matomo.tag-manager-versions.publish` releases a saved version to an environment. Matomo then rewrites the container
script of that environment, and the visitors of every website that embeds it run the new script on their next page
load. Every environment is such a script, so every publication is checked; `preview` belongs to Matomo's preview mode,
is no environment of this method, and is refused before any request.

- Rights: a write right; for `live` the capability `tagmanager_publish_live_container`; for another environment that
  capability or `tagmanager_use_custom_templates` (Matomo's own rule). `publishable-environments` of
  `matomo.tag-manager-catalog.list` shows which environments a token may publish to.
- The check reads the export of the version and refuses before the write request, quoting no provider text, when:
  - the version is the draft (revision 0), which can change between the check and the release; save it with
    `matomo.tag-manager-versions.create` and publish that version;
  - the export cannot be checked completely: it exceeds a limit of the export (see `truncated`), names no matching
    revision, or names a type in another form than a plain text;
  - a tag has another type than `Matomo` or `ThemeColor`: `ThemeColor` loads and sends nothing, and the address of
    `Matomo` is checked below, while other tag types, `CustomHtml` and `CustomImage` included, can run or load code
    or send data to another address, and a plugin can add more;
  - a variable or trigger has a type outside the plugin's own templates, or a variable is `CustomJsFunction` or
    `CustomRequestProcessing`, which run code from their parameters. A plugin can add templates with code of their
    own, so an unknown type cannot be checked;
  - a variable of type `MatomoConfiguration` loads the tracker from or sends to another address than the connection's
    base URL: `matomoUrl` must be the base URL with `https`, the same host and installation path, and at most a
    closing slash; a protocol-relative or other address, or one built from another variable, is refused. Its
    `idSite` must be the `idSite` of the call and `jsEndpoint` and `trackingEndpoint` one of Matomo's own files (not
    `custom`). The rules the variable tools apply to such a variable hold as well. Every such variable of the
    version is checked, used or not;
  - a `Matomo` tag does not name exactly one `MatomoConfiguration` variable of the version as `{{name}}` (compared
    without regard to case, as Matomo's database may).
- Matomo itself refuses an unknown environment, a missing right, or a version of another container or website; the
  refusal is reported as such and nothing was published.
- A saved version is not locked: Matomo lets a user with a write right change the tags, triggers, and variables of
  any version that is not released to `live`, and one with the right to publish there any version. A change made
  between the check and the release is therefore not prevented and is published with it. After the release the
  export is read once more, never a second time, and compared with the checked one in all its content, dates
  included. A difference, or a re-read that fails or cannot be checked completely, is an error that says the
  version was published. The re-read makes a change visible; it neither prevents it nor undoes the release. A
  publication is idempotent in effect and replaces the environment's previous release.

Importing a version and the preview mode are not part of these tools.
