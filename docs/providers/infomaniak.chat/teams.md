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

- `teams.get` and `team-members.*` take a bound `team_id`, checked locally. A team's invitation ID lets
  anyone join the team without an invitation and no read tool that a connection must name in `tools` exists for
  teams, so it is never returned. Email and allowed domains come as kChat reports them to the token; kChat masks
  them for non-administrators. `team-members.roles` changes only a live member of that team.
