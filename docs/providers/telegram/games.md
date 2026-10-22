---
description: >
  Describes the Telegram game tools.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Games

`telegram.games.send` (`create`), `telegram.games.set-score` (`update`), and `telegram.games.high-scores` (`read`)
send a game registered with BotFather, set a player's score, and list the scores around a player, only in a chat
bound by its numeric ID. Scores carry the data class `telegram-member-data`; inline game messages and keyboards are
not offered.
