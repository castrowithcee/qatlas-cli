package infomaniakchat

import (
	"context"
	"net/http"
)

// previewGuard removes what a message shows about channels the connection cannot reach: permalink previews,
// the previewed_post prop, and channel mentions. It follows what Mattermost does for a user without read
// access. Proof is gathered lazily and at most once per distinct channel (permalinks) or team (mentions)
// for the lifetime of one guard, so one guard serves one call. Whatever cannot be proven reachable is
// removed, and nothing removed is named anywhere.
type previewGuard struct {
	c   *Client
	ctx context.Context
	op  string

	channels   map[string]bool
	teamsRead  bool
	teamIDs    map[string]string // team name to ID, bound teams only
	teamScopes map[string]map[string]*channelJSON
}

func (c *Client) newPreviewGuard(ctx context.Context, op string) *previewGuard {
	return &previewGuard{c: c, ctx: ctx, op: op, channels: map[string]bool{}, teamScopes: map[string]map[string]*channelJSON{}}
}

// guardEntries applies a fresh guard to every entry.
func (c *Client) guardEntries(ctx context.Context, op string, entries []MessageEntry) {
	g := c.newPreviewGuard(ctx, op)
	for i := range entries {
		g.apply(&entries[i])
	}
}

// channelReachable proves a channel with the same check as every other channel-addressed operation, after
// the local channel allow-list.
func (g *previewGuard) channelReachable(channelID string) bool {
	if !validMattermostID(channelID) || !g.c.scope.allowsChannel(channelID) {
		return false
	}
	ok, known := g.channels[channelID]
	if !known {
		ok = g.c.verifyChannelScope(g.ctx, g.op, channelID) == nil
		g.channels[channelID] = ok
	}
	return ok
}

// mentionReachable proves a mentioned channel by its handle and team name against the reachable channels of
// the bound team of that name.
func (g *previewGuard) mentionReachable(name, teamName string) bool {
	if name == "" || teamName == "" {
		return false
	}
	if !g.teamsRead {
		g.teamsRead = true
		g.teamIDs = map[string]string{}
		var teams []teamJSON
		if g.c.do(g.ctx, g.op, http.MethodGet, "/api/v4/users/me/teams", nil, nil, &teams, false) == nil {
			for _, team := range teams {
				if g.c.scope.allowsTeam(team.ID) {
					g.teamIDs[team.Name] = team.ID
				}
			}
		}
	}
	teamID := g.teamIDs[teamName]
	if teamID == "" {
		return false
	}
	reachable, known := g.teamScopes[teamID]
	if !known {
		reachable, _ = g.c.reachableChannels(g.ctx, g.op, teamID)
		g.teamScopes[teamID] = reachable
	}
	for _, ch := range reachable {
		if ch.Name == name {
			return true
		}
	}
	return false
}

// filterMentions keeps only the reachable entries of a channel_mentions prop and drops the prop when none is
// left. A value of an unexpected shape is not provable and is dropped.
func (g *previewGuard) filterMentions(props map[string]any) {
	raw, present := props["channel_mentions"]
	if !present {
		return
	}
	mentions, _ := raw.(map[string]any)
	for name, value := range mentions {
		entry, _ := value.(map[string]any)
		teamName, _ := entry["team_name"].(string)
		if !g.mentionReachable(name, teamName) {
			delete(mentions, name)
		}
	}
	if len(mentions) == 0 {
		delete(props, "channel_mentions")
	}
}

// permalinkReachable decides on one permalink embed from the channel ID its data names. The embedded post
// is reduced to what Mattermost keeps of it: reachable channel mentions, and no nested previews.
func (g *previewGuard) permalinkReachable(data map[string]any) bool {
	post, _ := data["post"].(map[string]any)
	outer, _ := data["channel_id"].(string)
	inner, _ := post["channel_id"].(string)
	if outer != "" && inner != "" && outer != inner {
		return false
	}
	channelID := outer
	if channelID == "" {
		channelID = inner
	}
	if !g.channelReachable(channelID) {
		return false
	}
	if post != nil {
		if props, ok := post["props"].(map[string]any); ok {
			delete(props, "previewed_post")
			g.filterMentions(props)
			if len(props) == 0 {
				delete(post, "props")
			}
		}
		if metadata, ok := post["metadata"].(map[string]any); ok {
			delete(metadata, "embeds")
			if len(metadata) == 0 {
				delete(post, "metadata")
			}
		}
	}
	return true
}

// apply filters one message entry in place.
func (g *previewGuard) apply(entry *MessageEntry) {
	keptPermalink := false
	if entry.Metadata != nil && len(entry.Metadata.Embeds) > 0 {
		kept := entry.Metadata.Embeds[:0]
		for _, embed := range entry.Metadata.Embeds {
			if embed.Type == "permalink" {
				if !g.permalinkReachable(embed.Data) {
					continue
				}
				keptPermalink = true
			}
			kept = append(kept, embed)
		}
		entry.Metadata.Embeds = kept
		if len(kept) == 0 {
			entry.Metadata.Embeds = nil
			if entry.Metadata.Emojis == nil && entry.Metadata.Files == nil && entry.Metadata.Images == nil &&
				entry.Metadata.Reactions == nil {
				entry.Metadata = nil
			}
		}
	}
	if entry.Props == nil {
		return
	}
	if !keptPermalink {
		delete(entry.Props, "previewed_post")
	}
	g.filterMentions(entry.Props)
	if len(entry.Props) == 0 {
		entry.Props = nil
	}
}
