---
description: >
  Describes the Penpot webhook tools that list, update, and delete the webhooks of a team: what is withheld, why no
  tool creates a webhook, and the team-wide allow-list refusal.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Penpot: webhooks

The three webhook tools work on the webhooks of one team and take `team_id`, which must be a bound team (checked before
the credential is resolved and before any request; the refusal never names the team). Qatlas creates no permanent
access or data paths to the outside (webhooks, invitations, public links); reading, pausing, revoking, and deleting
remain, so no tool creates a webhook, changes a webhook's target URL, or invites a person.

- `webhooks.list` answers at most 100 webhooks. **The target URL is never returned, only its host name**, since a URL
  may carry a secret in its path or query. Webhook secrets are not exposed by Penpot's list and are never handled.
- `webhooks.update` has no `url` argument: it reads the stored URL from `get-webhooks` of the bound team and sends it
  back unchanged, so the target never changes and never leaves the client. Penpot resets the error counters.
- `webhooks.delete` deletes a webhook.

A webhook ID is bound before an update or delete: it must appear in `get-webhooks` of the given bound team, else it is
refused without naming it. **Webhooks belong to the whole team, so a connection with a project or file allow-list
refuses update and delete**; listing is still allowed.

Changes follow [Changes](README.md#changes), except that an unreadable answer does not make the outcome uncertain
here; list the webhooks before trying again.
