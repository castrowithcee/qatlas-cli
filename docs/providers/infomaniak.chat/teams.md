---
description: >
  Describes the Infomaniak kChat team, team member, and insights tools with their binding and behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-28
---

# Infomaniak kChat teams

The teams tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- `teams.get` and `team-members.*` take a bound `team_id`, checked locally. A team's invitation ID lets
  anyone join the team without an invitation and no read tool that a connection must name in `tools` exists for
  teams, so it is never returned. Email and allowed domains come as kChat reports them to the token.
  `team-members.roles` changes only a live member of that team.
- `insights.top` with `team_members` ranks nothing: it lists the team members who joined in the time range,
  and its `total` is kChat's count before unreachable users are left out. Ranked channels carry no `team_id`;
  the envelope's `team_id` names the one team read.
- `insights.top` with `reactions` ranks emoji over the whole team, so excluded channels count in it.
- Thread authors and new members outside the bound teams are dropped, or reduced to their ID where the thread
  itself stays. A thread shows its root message only while that message is not deleted.

The token rights are listed under [Teams](setup.md#teams).
