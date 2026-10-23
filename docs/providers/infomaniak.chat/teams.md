---
description: >
  Describes the Infomaniak kChat team, team member, and insights tools with their binding and behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-23
---

# Infomaniak kChat teams

The teams tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- `teams.get` and `team-members.*` take a bound `team_id`, checked locally. `teams.get` never returns the
  invitation ID, email, or allowed domains; `team-members.roles` changes only a live member of that team.
