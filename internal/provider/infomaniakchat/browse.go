package infomaniakchat

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// browsePageSize is the number of entries of one page of choices. kChat answers the teams and channels of
	// a token as one complete array, so the provider pages it itself.
	browsePageSize = 50
	// maxBrowsePage bounds a cursor well above any real listing.
	maxBrowsePage = 100000
	// maxChoiceName keeps a name well inside the bound the caller applies to entry texts.
	maxChoiceName = 200
)

// openUnbound returns a client for choosing targets: the connection may still be a draft without a team.
func openUnbound(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	return connect(ctx, resolved, secrets, red, nil, scope{})
}

// choiceName makes a name from the provider fit for an entry: the display name, else the handle, else the
// ID, without control characters and cut to a bounded length. It stays text; nothing interprets it.
func choiceName(display, name, id string) string {
	for _, candidate := range []string{display, name} {
		clean := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == utf8.RuneError {
				return -1
			}
			return r
		}, candidate))
		if clean == "" {
			continue
		}
		if len(clean) > maxChoiceName {
			clean = clean[:maxChoiceName]
			for !utf8.ValidString(clean) {
				clean = clean[:len(clean)-1]
			}
		}
		return clean
	}
	return id
}

func teamEntryOf(t teamJSON) config.TargetEntry {
	return config.TargetEntry{Target: teamPrefix + t.ID, Name: choiceName(t.DisplayName, t.Name, t.ID),
		Kind: "team", HasChildren: true}
}

func channelTargetOf(ch *channelJSON) config.TargetEntry {
	return config.TargetEntry{Target: channelPrefix + ch.ID, Name: choiceName(ch.DisplayName, ch.Name, ch.ID), Kind: "channel"}
}

// ListTargets reads one page of the teams of the token or of the channels of one team. Neither level
// filters by the connection's current targets, and every read counts against the provider's limiter like any
// other. A search text reads through the channel search of the provider.
func ListTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	page := 1
	if req.Cursor != "" {
		n, err := strconv.Atoi(req.Cursor)
		if err != nil || n < 1 || n > maxBrowsePage || strconv.Itoa(n) != req.Cursor {
			return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
		}
		page = n
	}
	var teamID string
	switch req.Kind {
	case "team":
	case "channel":
		kind, id, err := parseTarget(strings.TrimSpace(req.Parent))
		if err != nil || kind != "team" {
			return config.TargetPage{}, invalidRequest("the parent of a channel must be team/TEAM_ID")
		}
		teamID = id
		if req.Query != "" && (req.Cursor != "" || !validSearchTerm(req.Query)) {
			return config.TargetPage{}, invalidRequest("the search text of a channel is not usable")
		}
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one this provider offers")
	}
	client, err := openUnbound(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	switch {
	case teamID == "":
		return client.listTeamTargets(ctx, page)
	case req.Query != "":
		return client.searchChannelTargets(ctx, teamID, req.Query)
	}
	return client.listChannelTargets(ctx, teamID, page)
}

func nextPage(pages, page int) string {
	if pages > page {
		return strconv.Itoa(page + 1)
	}
	return ""
}

func (c *Client) listTeamTargets(ctx context.Context, page int) (config.TargetPage, error) {
	var teams []teamJSON
	if err := c.do(ctx, "list team targets", http.MethodGet, "/api/v4/users/me/teams", nil, nil, &teams, false); err != nil {
		return config.TargetPage{}, err
	}
	valid := make([]teamJSON, 0, len(teams))
	for _, t := range teams {
		if validMattermostID(t.ID) {
			valid = append(valid, t)
		}
	}
	window, pages, _ := windowOf(valid, page, browsePageSize)
	entries := make([]config.TargetEntry, 0, len(window))
	for _, t := range window {
		entries = append(entries, teamEntryOf(t))
	}
	return config.TargetPage{Entries: entries, Next: nextPage(pages, page)}, nil
}

// listChannelTargets pages the channels of one team the token is a member of. Archived channels and direct
// or group channels, which belong to no team, are not offered.
func (c *Client) listChannelTargets(ctx context.Context, teamID string, page int) (config.TargetPage, error) {
	channels, err := c.teamChannels(ctx, "list channel targets", teamID)
	if err != nil {
		return config.TargetPage{}, err
	}
	offered := make([]*channelJSON, 0, len(channels))
	for i := range channels {
		ch := &channels[i]
		if validMattermostID(ch.ID) && ch.TeamID == teamID && ch.DeleteAt == 0 && ch.Type != channelDirect && ch.Type != channelGroup {
			offered = append(offered, ch)
		}
	}
	window, pages, _ := windowOf(offered, page, browsePageSize)
	entries := make([]config.TargetEntry, 0, len(window))
	for _, ch := range window {
		entries = append(entries, channelTargetOf(ch))
	}
	return config.TargetPage{Entries: entries, Next: nextPage(pages, page)}, nil
}

// searchChannelTargets uses the channel search of the provider, which finds public channels the token is a
// member of; a private channel appears in the list but not in a search.
func (c *Client) searchChannelTargets(ctx context.Context, teamID, term string) (config.TargetPage, error) {
	found, err := c.SearchChannels(ctx, teamID, term)
	if err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(found.Channels))
	for _, ch := range found.Channels {
		if validMattermostID(ch.ID) {
			entries = append(entries, config.TargetEntry{Target: channelPrefix + ch.ID,
				Name: choiceName(ch.DisplayName, ch.Name, ch.ID), Kind: "channel"})
		}
	}
	return config.TargetPage{Entries: entries, ServerSearch: true}, nil
}

// ResolveTargetURL turns a channel URL of the connection's own instance, https://TEAM.kchat.infomaniak.com/
// TEAM/channels/NAME, into the team and the channel. The first path segment is the team handle and the last
// the channel handle; neither is an ID, so the team is found among the teams of the token and the channel
// by its handle within that team. A URL of another host, another form, or a team or channel the token does
// not show gives nothing.
func ResolveTargetURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	_, rawURL string) ([]config.TargetEntry, error) {
	if resolved == nil {
		return nil, nil
	}
	instance, err := parseInstance(resolved.BaseURL)
	if err != nil {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		"https://"+strings.ToLower(u.Hostname()) != instance {
		return nil, nil
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) != 3 || segments[1] != "channels" || !validTeamLabel(segments[0]) ||
		!channelNamePattern.MatchString(segments[2]) {
		return nil, nil
	}
	client, err := openUnbound(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var teams []teamJSON
	if err := client.do(ctx, "resolve target url", http.MethodGet, "/api/v4/users/me/teams", nil, nil, &teams, false); err != nil {
		return nil, err
	}
	var team *teamJSON
	for i := range teams {
		if teams[i].Name == segments[0] && validMattermostID(teams[i].ID) {
			team = &teams[i]
			break
		}
	}
	if team == nil {
		return nil, nil
	}
	var ch channelJSON
	path := "/api/v4/teams/" + url.PathEscape(team.ID) + "/channels/name/" + url.PathEscape(segments[2])
	if err := client.do(ctx, "resolve target url", http.MethodGet, path, nil, nil, &ch, false); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) &&
			(providerErr.Class == provider.ClassNotFound || providerErr.Class == provider.ClassPermission) {
			return nil, nil
		}
		return nil, err
	}
	if !validMattermostID(ch.ID) || ch.TeamID != team.ID || ch.DeleteAt != 0 || ch.Name != segments[2] ||
		ch.Type == channelDirect || ch.Type == channelGroup {
		return nil, nil
	}
	return []config.TargetEntry{teamEntryOf(*team), channelTargetOf(&ch)}, nil
}

// reaches says which tools a binding offers. A tool that is instance-wide or team-wide is refused at its call
// as soon as the connection holds a channel target, so it is not offered then; the refusal at the call
// stays as it is.
func reaches(tool string, targets []string) (bool, []string) {
	if !teamWideTools()[tool] {
		return true, nil
	}
	for _, target := range targets {
		if strings.HasPrefix(strings.TrimSpace(target), channelPrefix) {
			return false, nil
		}
	}
	return true, nil
}

func teamWideTools() map[string]bool {
	ids := []string{botsList.ID, botsGet.ID, botsUpdate.ID, botsDisable.ID, botsEnable.ID, emojiList.ID,
		emojiCreate.ID, emojiDelete.ID, rolesGet.ID, channelsCreate.ID, commandsList.ID, commandsGet.ID,
		commandsUpdate.ID, commandsDelete.ID, directOpen.ID, groupMessagesOpen.ID}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
