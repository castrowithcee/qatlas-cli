---
description: >
  Describes Nextcloud Deck tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Nextcloud Deck

Group `deck`: boards, stacks, cards, comments, and attachments.

The tool group `deck` reads boards (labels, sharing, members), stacks with cards, card comments, and attachments, and
changes all of them; setup profile `deck-read` holds four board reads, and no profile holds a change. A `deck` target
binds every board, `deck/BOARD_ID` one board; an unbound board is refused locally, unnamed, before any credential access
or request, and listings drop unbound and deleted boards. A stack, card, label, sharing entry, or target counts only if
a read of the bound board shows it. Provider text is untrusted, capped, and marked `truncated` when cut. Only
`deck_file` attachments are downloaded or deleted; uploads take a `local_path` file up to 64 MiB.

Every change needs `confirm` and sends one request after at most two reads. `deck.boards.create` needs the general
`deck` target. Board, stack, label, and sharing changes refuse without the manage right; card changes leave the edit
right to Deck. `deck.cards.update` refuses a card changed since its `last_modified`; an archived card can only be
restored. Cards take only the board's labels and user members; `deck.acl.*` shares with users and groups only and
refuses team and owner entries. Comments work on active cards only; update and delete reach only own comments among
the newest 100. Deck deletes boards, cards, and attachments softly, with no Qatlas restore; a deleted stack takes its
cards. Every `delete` and `deck.acl` tool needs a tools list.
