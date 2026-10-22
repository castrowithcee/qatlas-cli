---
description: >
  Describes the Make tools for the bound team and its organization: the argument-free team reads, the teams of an
  organization, organization master data and members, the read-only financial data, and the team and organization
  variables, with their Make scopes, profiles, and refusals.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Make: team and organization

Which of these tools a binding reaches depends on its mode; see [Scope](README.md#scope) and
[Choosing targets](README.md#choosing-targets). Errors follow [Errors](README.md#errors).

## The bound team and its organization

`make.team.get`, `make.team.usage`, `make.team.members`, and `make.organization.get` read the bound team and its
organization (team mode only). The `team` profile needs `teams:read`; the `organization` profile needs
`organizations:read` and `teams:read`, because the organization is found through the bound team.

The four tools take no arguments and never accept a team or organization ID. The team
is the connection's own target; `make.organization.get` takes the `organizationId` from the bound team's own
answer. A configured `organization/ID` target is checked against that answer, and a mismatch is refused as an
invalid request that never names the other organization. `make.team.members` lists user id and role id only, without
names or email addresses. Role names are not resolved, because that needs the separate `user:read` scope.

`make.organization.get` applies the team boundary to what it returns: the bound team keeps its name, while other
teams of the organization and all private spaces lie outside the targets and are reported by id alone. Make does not
document the keys of `license`; only its flat scalar values are reported (nested values are dropped, at most 64
entries), and an integer keeps its exact value. Lists are capped and `truncated` marks a cut. Text values are
untrusted data.

## Teams of an organization

`make.teams.list`, `.create`, `.update`, and `.delete` work on the teams of the bound organization, in
organization mode only. The `org-admin` profile (list, create, update) needs `teams:read` and `teams:write`, as does
the separately offered delete. None takes an organization argument; update and delete take `team_id`, which is read
live (`GET /teams/{teamId}`) and bound to the connection's organization through its `organizationId` before
anything changes. A team of another organization, and a personal space, is refused as an invalid request
without naming it, and no changing request is sent.

`make.teams.list` pages by `offset` and `limit` and sets `has_more` when a page came back full, because Make reports no
total count. It leaves personal spaces out unless `include_private_spaces` is set; those carry `type: personal`, and
update and delete still refuse them. Make documents no values for the sort field; only the direction is offered.

Changes need their own confirmation, send exactly one changing request, and are never retried; a timeout,
a connection reset, a 5xx, or an unreadable answer is reported as uncertain. **Deleting a team also deletes all of
its data: scenarios, webhooks, and custom team variables.** Qatlas does not look them up. `confirmed: true` is a
required argument and the only way Make's `confirmed` flag is sent. The tool is in no profile and is offered only
when a connection's `tools` list names it. The profile `org-admin` holds list, create, and update. Team names
are untrusted data.

## Organization master data

`make.organization.update` (organization mode only) changes the bound organization's `name`, `country_id`, or
`timezone_id` with one `PATCH /organizations/{organizationId}` (`organizations:write`). The organization is always the
connection's target; plan, license, and payment fields are not offered. The answer must report the bound organization,
otherwise the result is uncertain. It needs its own confirmation, sends exactly one request, is never retried, and
is in the `org-admin` profile.

`make.organization.members` reads the organization role list once, then
`GET /users?organizationId=&organizationRoleId=` per role (`user:read`), and returns at most 20 roles and 200 members,
`truncated` when more may exist. Names and emails are personal data and untrusted. It is in `org-admin`; changing
roles and removing members are not offered.

## Financial data (read only)

`make.organization.subscription`, `make.organization.usage`, and `make.organization.payments` (organization mode
only, all `organizations:read`) read `GET /organizations/{organizationId}/subscription`, `/usage`, and
`/payments` of the bound organization (checked 2026-10-06 against developers.make.com, not a live account). The
subscription answers plan id and name, next billing date, and pause state; usage the daily operations, data
transfer, and centicredits of Make's last 30 days; payments `invoice_number`, `created`, `type`, `status`,
`amount_total`, `currency`, `period_from`, and `period_to`, newest first, with `offset` and `limit` (1 to 100, 25
when omitted; `has_more` is true when a page came back full). Payment methods, the plan price, and invoice links
are never returned, and no plan change, payment method, discount, or other write is offered. The tools sit in
their own profile `org-billing-read`, not in `org-admin`. Text values are untrusted. A missing scope answers
403 and names `organizations:read`.

## Team variables

`make.team-variables.list`, `.create`, `.update`, and `.delete` work on the variables of the bound team. None takes a
team argument: the team is always the connection's own target. A connection with a scenario allow-list reaches none of
them, refused before any secret is read. Organization variables are in the [next section](#organization-variables).
`list` needs `team-variables:read`, `create` needs `team-variables:write`; `update` and `delete` need
`team-variables:write` and `team-variables:read` (the variable is listed first).

Source: developers.make.com API reference, Teams, team variables (checked 2026-10-05 against the published
reference, not a live account). Types are `number` (1), `text` (2, Make's string), `boolean` (3), and `date`
(4, ISO 8601; only an RFC 3339 timestamp or `YYYY-MM-DD` is accepted, an assumption). A name has 1 to 128
letters, digits, `$`, or `_` (Make documents the characters, not the length); a text value at most 4096 bytes;
`null` is refused. Values may hold secrets or personal data (sensitivity `make-team-variables-may-hold-secrets`)
and are untrusted; a listed value is capped like other payloads, with `truncated` set when anything was cut, at
most 500 variables per list.

Without the `customVariables` license (see `make.organization.get`) Make reports only its system variables and
answers a delete with 404. The list therefore reports `custom_count` and `system_count`: `custom_count` 0 can mean
that the feature is unavailable. `update` and `delete` first list the variables and refuse a system variable or an
unknown name as an invalid request without sending the change; the reference does not say whether Make itself
would refuse a system variable. Renaming and the history endpoint are not offered, and `update` always sends
type and value together, as the reference asks.

`delete` needs the argument `confirmed: true`, which is what sets Make's `confirmed=true`; the reference states
that the call fails without it. Scenarios that read the variable by name lose its value, and Qatlas does not look
them up, so check them first. It is in no profile and is offered only when a connection's `tools` list names it.
The profiles are `teamvariables-read` and `teamvariables-manage` (list, create, update); neither is recommended.
Changes need their own confirmation, send exactly one changing request, and are never retried; a timeout, a
connection reset, a 5xx, or an unreadable answer is reported as uncertain.

## Organization variables

`make.organization-variables.list`, `.create`, `.update`, and `.delete` work on the variables of the organization
a connection in organization mode is bound to; a team connection is refused before any secret is read. None
takes an organization argument. They behave like the [team variables](#team-variables): same names, types, caps,
validation, system variable refusal, and uncertain-result handling. `list` needs `organization-variables:read`,
`create` `organization-variables:write`, and `update` and `delete` both of them.

Source: developers.make.com API reference, Organizations (checked 2026-10-06 against the published reference,
not a live account). The list answer is read as an array under `organizationVariables`, an assumption from the
team variables. `delete` needs `confirmed: true`, is in no profile, and is offered only when a connection's
`tools` list names it. The profiles are `organizationvariables-read` and `organizationvariables-manage` (list,
create, update); neither is recommended. A 403 names `organization-variables:read` or `:write`. Sensitivity is
`make-organization-variables-may-hold-secrets`; values are untrusted.
