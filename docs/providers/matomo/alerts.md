---
description: >
  Describes the Matomo custom-alert tools: listing and reading the token's own alerts, their triggered entries and past values, and deleting an alert, the binding of an alert that watches several websites to the connection's targets, the hidden Microsoft Teams address, and the deliberately missing create and edit tools.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Matomo custom alerts

The custom-alert tools of the [Matomo provider](README.md). The profiles are described under
[Setup profiles](README.md#setup-profiles). All five tools need the CustomAlerts plugin; an instance without it
refuses them as an unsupported capability.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.alerts.list` | reads the alerts of the token's user whose websites all lie inside the targets (profile `live`) |
| `matomo.alerts.get` | reads one own alert (profile `live`) |
| `matomo.alerts.get-past-values` | computes the value an own alert watches per website, for the current or an earlier period (profile `live`) |
| `matomo.alerts.list-triggered` | reads the entries of alerts that fired for the token's login (profile `live`) |
| `matomo.alerts.delete` | deletes one own alert (effect `delete`, only via `tools`) |

An alert watches one metric of one report on several websites and notifies by e-mail, phone, Slack, or Microsoft
Teams. The tools to create and to edit an alert do not exist, because they set up permanent paths of data to
recipients outside Matomo. Matomo has no pause for alerts; Qatlas reads and deletes.

The read tools are in the profile `live`, not in `read`, because an alert carries the owner's login and the
recipients (additional e-mail addresses, phone numbers, Slack channel). Names, descriptions, conditions, and
recipients are untrusted provider data and are shortened.

- `matomo.alerts.list` returns the alerts of the token's own login. For a Super User, `ifSuperUserReturnAllAlerts`
  returns the alerts of all users. `idSite` narrows the list to one website. Matomo has no paging for it, so Qatlas
  cuts the batches and continues with `next_cursor`.
- `matomo.alerts.get` and `matomo.alerts.get-past-values` work only for the token's own alerts, also for a Super
  User; another user's alert is refused as not owned. The past value is computed by Matomo from the report of every
  website of the alert, which can take time; `subPeriodN` counts periods back, 0 is the current one.
- `matomo.alerts.list-triggered` returns the entries of the token's own login with the alert definition as it was
  when the alert fired.
- `matomo.alerts.delete` removes the alert for good and cannot restore it; its triggered entries stay.

The stored Microsoft Teams address is a credential. No tool returns it and no error quotes it; the alert shows only
`ms_teams_webhook_set`, whether one is stored.

## Targets

An alert has no single website: it watches the websites of `id_sites`, and Matomo shows it with all of them when
any is requested. Qatlas follows that. `list` shows an alert when at least one of its websites is inside the
targets, and with `idSite` when it watches that website; `id_sites` stays complete, so websites outside the targets
appear as IDs only. In `list-triggered` only the website of the entry counts. `get`, `get-past-values`, and
`delete` take `idSite`, which must be inside the targets, and `idAlert`; they read the alert once and refuse it as
not found, without telling whether it exists, when `idSite` is not one of its websites. Further websites outside
the targets do not matter. `get-past-values` returns the values of websites inside the targets only. Computing the
values or deleting follows only after that read; an alert is not a container of websites, and Matomo checks only
who owns it for the deletion.

`delete` needs the confirmation of the caller and is sent exactly once. After a result that is unclear (no answer in
time, a reset connection, an answer Qatlas cannot read) the error says the change may have been applied and is
never repeated; read the alert with `matomo.alerts.get` before trying again.
