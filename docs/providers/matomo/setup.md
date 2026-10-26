---
description: >
  Guides a user without special knowledge through setting up the Matomo provider: creating an authentication token, adding it to Qatlas, checking the connection, the Matomo rights and plugins each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Matomo

This page shows how to connect Qatlas to Matomo, which rights the Matomo user behind the token needs, and what to
do when Matomo refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Matomo provider](README.md) and the page of each tool group; this page names only the rights.

**The token decides what is possible at all.** A Matomo token has no scopes to tick: it carries the rights of the
Matomo user who created it, per website (view, write, or admin) or Super User rights. The rights you set in Qatlas
(the connection's `tools`, `effects`, and `targets`) limit it further. A call works only if both allow it. A
connection can never do more than the user, and the token never widens the connection. To give a connection fewer
rights, create the token for a Matomo user that has fewer rights.

## What you need

- A Matomo user with the rights from [Rights per tool group](#rights-per-tool-group) for the tools you will use.
  Use a separate user for a connection that should only read; do not use your own Super User.
- The address of your Matomo installation: `https://HOST[/PATH]`, or `https://SUBDOMAIN.matomo.cloud` for Matomo
  Cloud. There is no default; see [Configuration](README.md#configuration).
- Matomo 5 or newer. Older releases are refused.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- For some privacy tools, also the Matomo password of that user; see
  [Password confirmation](#password-confirmation).

## Create a token

In Matomo, open **Administration** (the gear icon), then **Personal**, then **Security**. Matomo changes its pages
from time to time; look for these names.

1. Click **Create New Token** and confirm your password.
2. Enter a **Description** you will recognize, for example `qatlas-reader`.
3. Leave **Only allow secure requests** on. Qatlas sends every request as `POST`, which such a token allows.
4. Matomo 6 also offers a **Token access level**. Choose the lowest level the connection needs, or **Inherit user
   access**. Matomo 5 offers none: the token has the rights of its user.
5. Optionally set an **Expiry date**. You replace the token when it ends.
6. Click **Create New Token** and copy the token at once. Matomo shows it only once.

Create the token while logged in as the user whose rights the connection should have. A token cannot give more
than that user holds.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `matomo`, the service with its address, a
   credential, the scope (websites), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$MATOMO_TOKEN" | qatlas credential set matomo-reader token
```

`matomo-reader` is the name of the credential in your configuration and `token` is its secret. To replace a
token that expired or was deleted, run the same command again with the new token. Run `qatlas config validate` to
check the configuration file. It contacts no provider.

### Password confirmation

The credential can hold a second, optional secret, `password-confirmation`: the Matomo password of the token's
user. Matomo asks for it on a few privacy tools even when a token is used; see the tools marked in
[Privacy](#privacy). Store it only for a connection that offers those tools:

```sh
printf %s "$MATOMO_PASSWORD" | qatlas credential set matomo-admin password-confirmation
```

Without it, those tools are refused before any request.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Matomo accepted the token,
the instance is Matomo 5 or newer, and each website the connection's targets name could be read.

**A passing test does not mean every tool works.** It proves the token and those websites, not write rights, not
Super User rights, and not any plugin. Matomo checks the rights again on every call. Compare the user's rights
with [Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is explained
under [Fixing errors](#fixing-errors).

## Rights per tool group

Find your tool group and give the Matomo user the right in the row.

- **View**, **write**, and **admin** are Matomo's rights per website. Each includes the one before it. "On the
  website" means the website the call names.
- **Super User** is Matomo's highest right and is not per website. Super User tools also need the `instance`
  target in the connection; see [Scope](README.md#scope).
- **Capabilities** are extra rights a role can hold, for example for Tag Manager. `matomo.roles.list` shows
  them. A capability is added to a user in Matomo's user administration.
- **Plugin**: the Matomo plugin must be installed and activated; without it the tools end as `unsupported-capability`.
- Unless a row says more, a tool that only reads needs a view right on the website it reads, and a change tool
  needs the right of its row. Tools offered only when a connection's `tools` list names them (for example
  deletes) need the same rights as their group.

### Sites and version

| Tools | Matomo right |
| --- | --- |
| Instance version | View on any website |
| Read websites, JavaScript tracking code, SEO metrics of a host | View on the website (any website for the version and the SEO metrics) |
| Image tracking code, opt-out code | None in Matomo; Qatlas limits them by the website targets |
| Record page views, events, searches, downloads and outlinks | None: no token is sent; tracking must be on for the website, and Qatlas limits the tools by the website targets |
| Website settings, tracking failures (list, delete) | Admin on the website |
| Delete all tracking failures | Admin; a Super User clears the whole table. Target `instance` |
| Update a website, add or replace alias URLs, set creation dates | Admin on every website named |
| Set the group of a website | Super User (Matomo ignores it for others) |
| Add or delete a website, list or rename website groups | Super User, target `instance` (deleting also the website target) |

### Reports, live data, and visitors

| Tools | Matomo right |
| --- | --- |
| Report catalog, reports, segment fields and values, distinct counts, glossary, row evolution | View on the website |
| Insights, transitions, AI chatbots | View on the website; plugins `Insights`, `Transitions`, `BotTracking` (the last from Matomo 5.12.0) |
| Website comparison | View on the websites of the token; plugin `MultiSites` |
| Start archiving a report | Super User, targets `instance` and the website |
| Visit log, live counters, latest visit time | View on the website; plugin `Live`; for the visit log the visits log must not be disabled for the website |
| Visitor profile, first visit | View on the website; visitor profiles must be enabled for the website |

### Goals

Plugin `Goals`.

| Tools | Matomo right |
| --- | --- |
| Read goals | View on the website |
| Create, replace, delete goals | Write on the website |
| Goal recommendations (list, scan, dismiss) | Write on the website, also to list; the goal recommendations feature of the instance |

### Saved segments

Plugin `SegmentEditor`. Matomo's `config.ini.php` settings can narrow what a user may do.

| Tools | Matomo right |
| --- | --- |
| Read segments | View on the website |
| Create a segment for a website | The access level of the instance setting `adding_segment_requires_access` in `[General]` (`view` by default) |
| Create a segment for all websites | Super User; `allow_adding_segments_for_all_websites = 0` switches these off |
| Replace, star, unstar, delete a segment | The creator of the segment, or a Super User; for a segment of all websites a Super User |
| Share a segment with all users | Super User |
| Segment that is pre-processed (`autoArchive`) | `enable_browser_archiving_triggering = 0`, and for all websites Super User |
| Segment that is not pre-processed | `enable_create_realtime_segments = 1` |

### Annotations

Plugin `Annotations`.

| Tools | Matomo right |
| --- | --- |
| Read and count annotations | View on the website |
| Add an annotation | View on the website in Matomo 5, write in Matomo 6 |
| Change an annotation | Write on the website, or being its author |
| Delete an annotation | Admin on the website, or being its author with a view right |
| Delete all annotations of a website | Super User, target `instance` |

### Custom dimensions

Plugin `CustomDimensions`.

| Tools | Matomo right |
| --- | --- |
| Read the dimensions of a website | View on the website |
| Read scopes with free slots and extraction sources | Write on some website |
| Add or change a dimension | Write on the website |

### Scheduled reports

Plugin `ScheduledReports`.

| Tools | Matomo right |
| --- | --- |
| List | A Super User sees all reports, any other user the user's own |
| Render as text | View on the website of the report |
| Pause, delete | The creator of the report, or a Super User |

### Instance settings and reference data

| Tools | Matomo right |
| --- | --- |
| Read global website settings: excluded IPs, user agents, referrers, search parameters, default currency | Admin on some website; target `instance` |
| Read global website settings: query parameters, URL fragments | View on some website; target `instance` |
| Read the default time zone | None; target `instance` |
| Change global website settings, archiving, trusted hosts, branding, lift brute-force blocks | Super User, target `instance` |
| Database statistics | Super User, target `instance`; plugin `DBStats` |
| Reference lists | View on some website; country codes need the plugin `UserCountry` |
| IP ranges | None in Matomo |
| Location of an IP address | View on any website |

### Users and roles

| Tools | Matomo right |
| --- | --- |
| Roles and capabilities Matomo offers | Admin on some website |
| Users of a website, access to a website | Admin on the website |
| Users of the instance, a user's websites | Admin on some website; target `instance` for the instance-wide mode |
| One user by login or email, the raw access of a user, users of a role | Super User, target `instance` |
| Check that a login or email exists | View on some website, target `instance` |
| Delete a user | Admin or Super User, target `instance`; an admin who is not a Super User can delete only a pending user the admin invited |
| End the web sessions of a user | Super User, target `instance` |
| Set a role, add or remove capabilities | Admin on every website named; no `instance` target |
| LDAP users (count, list) | Super User, target `instance`; plugin `LoginLdap` |

### Privacy

Plugin `PrivacyManager`; finding data subjects also needs the plugin `Live`. Tools marked "password" need the
secret `password-confirmation` (see [Password confirmation](#password-confirmation)).

| Tools | Matomo right |
| --- | --- |
| Find, export, or delete the visits of a person | Admin on every website named |
| Privacy settings of a website | Admin on the website |
| Privacy settings of the instance, column lists | Super User, target `instance` |
| Anonymize raw data, run the data purge | Super User, target `instance` (anonymizing also the websites); password |
| Set the retention of raw logs or reports, set the deletion schedule | Super User, target `instance`; password |
| Set IP anonymization | Super User, target `instance`; password only when `randomizeConfigId` is true |
| Enable or disable Do Not Track | Super User, target `instance` |
| Read compliance policies | Super User, target `instance` |
| Change compliance policies | Super User, target `instance`; granular tools also need Matomo 5.14.0 and a feature flag; see [Changing compliance policies](privacy.md#changing-compliance-policies) |

### SMS settings

Plugin `MobileMessaging`. All tools need the target `instance`.

| Tools | Matomo right |
| --- | --- |
| Read the SMS provider and the credit | The right to manage SMS credentials: Super User, unless management is delegated |
| Remove a phone number | The token's own user |
| Delete the SMS credentials | Matomo decides |
| Delegate the management | Super User |

### AI providers

Plugin `AIProviders`. Both tools need Super User rights and the target `instance`.

### Tag Manager

Plugin `TagManager`. The instance setting that restricts Tag Manager access to a higher role applies on top.

| Tools | Matomo right |
| --- | --- |
| Read containers, versions, tags, triggers, variables | View on the website |
| Create versions, change the draft, add a container | Write on the website |
| Update or delete a container | Write on the website and the right to publish |
| Delete or pause a tag that uses a custom template, change a trigger with one | The right to use custom templates |
| Environments you may publish to | The right to publish; without it `live` is left out |

## What happens without a right

Matomo reports the missing right as `permission`. The rest of the connection keeps working, because Matomo checks
every call alone. Two cases differ:

- A website the token cannot view is answered as `permission` when you pass its ID, and is missing from lists.
  With the target `site`, Qatlas learns this only from Matomo's answer.
- A user list without the admin right shows only the token's own user instead of failing.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas.

### Reading a refusal

```text
permission: this token may not use the requested website or function; check its rights for the website
provider answer (untrusted, not written by qatlas), HTTP 200:
  | You can't access this resource as it requires 'view' access for the website id = 3.
```

- The message is **Qatlas' own**; the second part is **Matomo's own answer**, untrusted, redacted, and shortened.
- Matomo translates its messages into the language of the token's user; Qatlas knows only the English ones. Set
  that user's interface language to English, or a refusal may appear only as `invalid-provider-response`.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Matomo rejected the token | The token is wrong, expired, deleted, or belongs to another Matomo; the instance accepts only secure tokens, or only listed IP addresses (`login_allowlist_ip`) | Create a new token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> token`. Keep **Only allow secure requests** on, and put the machine that runs Qatlas on the instance's `login_allowlist_ip`. |
| `auth`: the Matomo token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$MATOMO_TOKEN" \| qatlas credential set <credential> token`. |
| `auth`: Matomo rejected the token or the token has no view access to any website | Matomo answers an unusable token and a user without any view right the same way | Give the Matomo user at least a view right on one website, or create a new token. |
| `auth`: Matomo accepted the token only in the request body; check that the Authorization header reaches it | Matomo 5.4.0 or newer reads the token from the `Authorization` header, and a proxy in front of Matomo removes it | Let the header through on the proxy in front of Matomo. |
| `auth`: Matomo refused the password confirmation; check the secret role password-confirmation of this connection's credential | The stored password is wrong or is not the password of the token's user | Store the current password of that Matomo user: `qatlas credential set <credential> password-confirmation`. |
| `this tool needs the secret role password-confirmation ...` | The tool is one of the privacy tools marked "password" and the credential lacks the secret | Store the Matomo password of the token's user as `password-confirmation` ([Password confirmation](#password-confirmation)). |
| `permission`: this token may not use the requested website or function; check its rights for the website | The Matomo user lacks the view, write, admin, or Super User right of this call, or has no access to this website | Find the tool group in [Rights per tool group](#rights-per-tool-group). Give the user the right in Matomo. |
| `permission`: Matomo refused the operation for this token | The same, or Matomo answered HTTP 403, for example from a proxy or a firewall | Check the rights as in the row above, then the proxy in front of Matomo. |
| `permission` for a Super User tool | The user is only an admin | Use a token of a Super User, or leave the tool out of the connection. |
| `permission` on creating a segment | The user's access level is below `adding_segment_requires_access`, or `allow_adding_segments_for_all_websites = 0` is set | Raise the access of the user for that website, or change the setting in Matomo's `config.ini.php`. |
| `permission`: the compliance policy is controlled by the Matomo configuration file and cannot be changed through the API; change it there | The policy is fixed in `config.ini.php` | Change it in that file; no right of the user helps. |
| `unsupported-capability`: ... it needs the Matomo plugin ... to be activated | The plugin of the tool group is missing or off | Ask the Matomo Super User to activate the plugin in Matomo's plugin administration. The plugin is named in the message and in [Rights per tool group](#rights-per-tool-group). |
| `unsupported-capability`: ... it needs Matomo ... or newer | The instance is older than the tool needs | Update Matomo. The connection test reports a release before 5.0 the same way. |
| `unsupported-capability`: the instance does not provide the method this tool calls | The plugin or feature behind the method is missing or switched off, for example visitor profiles or the visits log of a website | Enable the plugin or the setting in Matomo; see the group in [Rights per tool group](#rights-per-tool-group). |
| `unsupported-capability`: Matomo's granular compliance feature is not enabled; ... | The feature flag is off | Switch it on as described in [Changing compliance policies](privacy.md#changing-compliance-policies). |
| `not-found`: Matomo does not hold this resource or does not show it to this token | The ID is wrong, **or** the user cannot see the website or object. Matomo answers both the same | Check the ID. Then give the user a view right on that website. |
| `invalid-request` naming the targets | The website or the instance is outside the connection's targets | Not a Matomo problem. Pass a website the connection allows, or add it or `instance`; see [Scope](README.md#scope). |
| `invalid-request`: a Matomo connection needs a base URL of the form https://HOST[/PATH], without user, query, or fragment | The service address is wrong | Use `https://HOST[/PATH]`; see [Configuration](README.md#configuration). |
| `rate-limited` | Matomo Cloud limits requests per IP address | Wait and retry. Do not let several programs use one IP address at once. |
| `unreachable` or `timeout` | Matomo or the network is down or slow | Retry a read. For a change, read the resource first, or a create makes a second one. |
