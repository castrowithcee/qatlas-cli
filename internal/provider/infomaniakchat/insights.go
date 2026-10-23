package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// insightKinds maps the insight kind to the fixed path segment; an argument is never used as a path part.
var insightKinds = map[string]string{
	"channels": "channels", "threads": "threads", "reactions": "reactions", "team_members": "team_members",
}

var insightRanges = map[string]bool{"today": true, "7_day": true, "28_day": true}

var insightChannelSchema = `{"type":"object","properties":{` +
	`"channel_id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},` +
	`"type":{"type":"string"},"message_count":{"type":"integer"}},` +
	`"required":["channel_id","name","display_name","type","message_count"],"additionalProperties":false}`

var insightUserSchema = `{"type":"object","properties":{"id":` + idSchema + `,"first_name":{"type":"string"},` +
	`"last_name":{"type":"string"},"nickname":{"type":"string"},"username":{"type":"string"},` +
	`"created_at":{"type":"string"},"last_picture_update":{"type":"integer"}},` +
	`"required":["id"],"additionalProperties":false}`

var insightThreadSchema = `{"type":"object","properties":{` +
	`"post_id":` + idSchema + `,"channel_id":` + idSchema + `,"reply_count":{"type":"integer"},` +
	`"participant_ids":{"type":"array","items":` + idSchema + `},` +
	`"channel_name":{"type":"string"},"channel_display_name":{"type":"string"},"post":` + messageEntrySchema + `,` +
	`"user_information":` + insightUserSchema + `},` +
	`"required":["post_id","channel_id","reply_count","participant_ids"],"additionalProperties":false}`

const insightReactionSchema = `{"type":"object","properties":{"emoji_name":{"type":"string"},` +
	`"count":{"type":"integer"}},"required":["emoji_name","count"],"additionalProperties":false}`

var insightMemberSchema = `{"type":"object","properties":{` +
	`"user_id":` + idSchema + `,"username":{"type":"string"},"first_name":{"type":"string"},` +
	`"last_name":{"type":"string"},"nickname":{"type":"string"},"position":{"type":"string"},` +
	`"joined_at":{"type":"string"}},` +
	`"required":["user_id","username"],"additionalProperties":false}`

var insightsTop = capability.Descriptor{
	ID:      Provider + ".insights.top",
	Version: 2,
	Title:   "Read Infomaniak kChat team insights",
	Description: "Read one page of the ranking of one bound team for today, 7 days, or 28 days: the top " +
		"channels, threads, or reactions, or the team members who joined in the time range. Channels and threads " +
		"outside the channels this connection may reach and members it may not reach are left out; the author " +
		"of a thread it may not reach shows only as an ID",
	Tags:     []string{"infomaniak", "kchat", "teams", "insights", "top", "activity"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,` +
		`"kind":{"type":"string","enum":["channels","threads","reactions","team_members"]},` +
		`"time_range":{"type":"string","enum":["today","7_day","28_day"]},` +
		`"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id","kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"kind":{"type":"string"},"time_range":{"type":"string"},` +
		`"channels":{"type":"array","items":` + insightChannelSchema + `},` +
		`"threads":{"type":"array","items":` + insightThreadSchema + `},` +
		`"reactions":{"type":"array","items":` + insightReactionSchema + `},` +
		`"members":{"type":"array","items":` + insightMemberSchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"},"total":{"type":"integer"}},` +
		`"required":["team_id","kind","time_range","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "kind", Description: "channels, threads, reactions, or team_members (the members who joined in the time range)", Required: true},
		{Name: "time_range", Description: "today, 7_day, or 28_day; today when omitted"},
		{Name: "page", Description: "1-based page of the ranking; the first page when omitted"},
		{Name: "limit", Description: "Entries per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) +
			" when omitted"},
	},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel identifier"},
		{Name: "name", Description: "Channel handle, untrusted data"},
		{Name: "display_name", Description: "Channel display name, untrusted data"},
		{Name: "type", Description: "kChat's own channel type, for example O (public) or P (private)"},
		{Name: "message_count", Description: "Messages of the channel in the time range"},
		{Name: "post_id", Description: "Root message of the thread"},
		{Name: "reply_count", Description: "Replies of the thread"},
		{Name: "participant_ids", Description: "Reachable users who took part in the thread"},
		{Name: "channel_name", Description: "Handle of the thread's channel, untrusted data"},
		{Name: "channel_display_name", Description: "Display name of the thread's channel, untrusted data"},
		{Name: "post", Description: "Root message of the thread as a message entry, untrusted data; left out when deleted"},
		{Name: "id", Description: "Identifier of the thread's author, in user_information"},
		{Name: "created_at", Description: "Creation time of the author's account in RFC 3339 in UTC, in user_information"},
		{Name: "last_picture_update", Description: "Milliseconds since the epoch of the author's last picture change, " +
			"in user_information"},
		{Name: "user_information", Description: "Author of the thread, untrusted data; names, handle, creation time, and " +
			"last picture update in milliseconds only for a reachable user, otherwise just the ID"},
		{Name: "emoji_name", Description: "Emoji name, untrusted data"},
		{Name: "count", Description: "Number of entries on this page after filtering; for a reaction, its uses"},
		{Name: "user_id", Description: "Member user identifier"},
		{Name: "username", Description: "Member handle, untrusted data"},
		{Name: "first_name", Description: "First name of the new member, untrusted data"},
		{Name: "last_name", Description: "Last name of the new member, untrusted data"},
		{Name: "nickname", Description: "Nickname of the new member, untrusted data"},
		{Name: "position", Description: "Position of the new member, untrusted data"},
		{Name: "joined_at", Description: "Time the member joined the team, RFC 3339 in UTC"},
		{Name: "total", Description: "Number of new members in the time range as kChat counts them, before unreachable " +
			"users are left out; only for team_members"},
		{Name: "team_id", Description: "Team that was read"},
		{Name: "kind", Description: "Ranking that was read"},
		{Name: "time_range", Description: "Time range that was read"},
		{Name: "page", Description: "Page that was read"},
		{Name: "has_more", Description: "True when kChat reports a further page"},
	},
	Examples: []capability.Example{{Description: "Read the most active channels of the last 7 days",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","kind":"channels","time_range":"7_day"}`)}},
}

type insightsArguments struct {
	TeamID    string `json:"team_id"`
	Kind      string `json:"kind"`
	TimeRange string `json:"time_range"`
	Page      int    `json:"page"`
	Limit     int    `json:"limit"`
}

// InsightChannel is a ranked channel.
type InsightChannel struct {
	ChannelID    string `json:"channel_id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Type         string `json:"type"`
	MessageCount int    `json:"message_count"`
}

// InsightThread is a ranked thread.
type InsightThread struct {
	PostID         string   `json:"post_id"`
	ChannelID      string   `json:"channel_id"`
	ReplyCount     int      `json:"reply_count"`
	ParticipantIDs []string `json:"participant_ids"`

	ChannelName        string        `json:"channel_name,omitempty"`
	ChannelDisplayName string        `json:"channel_display_name,omitempty"`
	Post               *MessageEntry `json:"post,omitempty"`
	UserInformation    *InsightUser  `json:"user_information,omitempty"`
}

// InsightUser is the author of a ranked thread; everything but the ID is set only for a reachable user.
type InsightUser struct {
	ID                string `json:"id"`
	FirstName         string `json:"first_name,omitempty"`
	LastName          string `json:"last_name,omitempty"`
	Nickname          string `json:"nickname,omitempty"`
	Username          string `json:"username,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	LastPictureUpdate int64  `json:"last_picture_update,omitempty"`
}

// InsightReaction is a ranked emoji.
type InsightReaction struct {
	EmojiName string `json:"emoji_name"`
	Count     int    `json:"count"`
}

// InsightMember is a team member who joined in the time range.
type InsightMember struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	Position  string `json:"position,omitempty"`
	JoinedAt  string `json:"joined_at,omitempty"`
}

// InsightsPage is one page of an activity ranking; only the list of the asked kind is set.
type InsightsPage struct {
	TeamID    string            `json:"team_id"`
	Kind      string            `json:"kind"`
	TimeRange string            `json:"time_range"`
	Channels  []InsightChannel  `json:"channels,omitempty"`
	Threads   []InsightThread   `json:"threads,omitempty"`
	Reactions []InsightReaction `json:"reactions,omitempty"`
	Members   []InsightMember   `json:"members,omitempty"`
	Page      int               `json:"page"`
	HasMore   bool              `json:"has_more"`
	Count     int               `json:"count"`
	Total     *int              `json:"total,omitempty"`
}

type insightItemJSON struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	DisplayName  string          `json:"display_name"`
	Username     string          `json:"username"`
	EmojiName    string          `json:"emoji_name"`
	Count        int             `json:"count"`
	MessageCount flexInt         `json:"message_count"`
	ChannelID    string          `json:"channel_id"`
	Participants json.RawMessage `json:"participants"`
	Post         json.RawMessage `json:"post"`

	ChannelName        tolerant[string] `json:"channel_name"`
	ChannelDisplayName tolerant[string] `json:"channel_display_name"`
	UserInformation    json.RawMessage  `json:"user_information"`
	FirstName          tolerant[string] `json:"first_name"`
	LastName           tolerant[string] `json:"last_name"`
	Nickname           tolerant[string] `json:"nickname"`
	Position           tolerant[string] `json:"position"`
	CreateAt           flexInt          `json:"create_at"`
}

func invokeInsightsTop(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read insights"
	var input insightsArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if _, ok := insightKinds[input.Kind]; !ok {
		return nil, invalidRequest("kind must be channels, threads, reactions, or team_members")
	}
	if input.TimeRange == "" {
		input.TimeRange = "today"
	}
	if !insightRanges[input.TimeRange] {
		return nil, invalidRequest("time_range must be today, 7_day, or 28_day")
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Limit == 0 {
		input.Limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.TopInsights(ctx, input.TeamID, input.Kind, input.TimeRange, input.Page, input.Limit)
}

// TopInsights reads one page of a team ranking with one fixed GET and never follows a further page. Channel
// and thread entries are kept only for reachable channels of the team, member entries and thread
// participants only for reachable users.
func (c *Client) TopInsights(ctx context.Context, teamID, kind, timeRange string, page, limit int) (*InsightsPage, error) {
	const op = "read insights"
	segment, ok := insightKinds[kind]
	if !ok {
		return nil, invalidRequest("kind must be channels, threads, reactions, or team_members")
	}
	query := url.Values{"time_range": {timeRange}, "page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(limit)}}
	var answer struct {
		HasNext    bool              `json:"has_next"`
		Items      []insightItemJSON `json:"items"`
		TotalCount json.RawMessage   `json:"total_count"`
	}
	path := "/api/v4/teams/" + url.PathEscape(teamID) + "/top/" + segment
	if err := c.do(ctx, op, http.MethodGet, path, query, nil, &answer, false); err != nil {
		return nil, err
	}
	result := &InsightsPage{TeamID: teamID, Kind: kind, TimeRange: timeRange, Page: page, HasMore: answer.HasNext}
	items := answer.Items
	switch kind {
	case "channels", "threads":
		var reachable map[string]*channelJSON
		if len(items) > 0 {
			var err error
			if reachable, err = c.reachableChannels(ctx, op, teamID); err != nil {
				return nil, err
			}
		}
		if kind == "channels" {
			for i := range items {
				it := &items[i]
				if reachable[it.ID] == nil {
					continue
				}
				result.Channels = append(result.Channels, InsightChannel{ChannelID: it.ID, Name: bounded(it.Name),
					DisplayName: bounded(it.DisplayName), Type: bounded(it.Type), MessageCount: int(it.MessageCount)})
			}
			result.Count = len(result.Channels)
			return result, nil
		}
		return c.insightThreads(ctx, op, result, items, reachable)
	case "reactions":
		for i := range items {
			if items[i].EmojiName == "" {
				continue
			}
			result.Reactions = append(result.Reactions, InsightReaction{EmojiName: bounded(items[i].EmojiName),
				Count: items[i].Count})
		}
		result.Count = len(result.Reactions)
	default:
		ids := make([]string, 0, len(items))
		for i := range items {
			if validMattermostID(items[i].ID) {
				ids = append(ids, items[i].ID)
			}
		}
		allowed, err := c.reachableIDs(ctx, op, ids)
		if err != nil {
			return nil, err
		}
		for i := range items {
			if allowed[items[i].ID] {
				it := &items[i]
				result.Members = append(result.Members, InsightMember{UserID: it.ID, Username: bounded(it.Username),
					FirstName: cleanText(it.FirstName.Value), LastName: cleanText(it.LastName.Value),
					Nickname: cleanText(it.Nickname.Value), Position: cleanText(it.Position.Value),
					JoinedAt: msToRFC3339(int64(it.CreateAt))})
			}
		}
		result.Count = len(result.Members)
		var total int
		if json.Unmarshal(answer.TotalCount, &total) == nil {
			result.Total = &total
		}
	}
	return result, nil
}

// insightThreadHead is the part of a ranked thread's root message the entry is keyed by.
type insightThreadHead struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	ReplyCount int    `json:"reply_count"`
}

// insightAuthorJSON is the user information kChat reports for the author of a ranked thread. Any member of
// another type reads as missing.
type insightAuthorJSON struct {
	ID                string           `json:"id"`
	FirstName         tolerant[string] `json:"first_name"`
	LastName          tolerant[string] `json:"last_name"`
	Nickname          tolerant[string] `json:"nickname"`
	Username          tolerant[string] `json:"username"`
	CreateAt          flexInt          `json:"create_at"`
	LastPictureUpdate flexInt          `json:"last_picture_update"`
}

// insightThreads keeps the threads of reachable channels, the reachable participants of each, and the
// author in full only when reachable.
func (c *Client) insightThreads(ctx context.Context, op string, result *InsightsPage, items []insightItemJSON,
	reachable map[string]*channelJSON) (*InsightsPage, error) {
	heads := make([]insightThreadHead, len(items))
	authors := make([]*insightAuthorJSON, len(items))
	participants := make([][]string, len(items))
	var all []string
	for i := range items {
		if json.Unmarshal(items[i].Post, &heads[i]) != nil {
			heads[i] = insightThreadHead{}
		}
		var author insightAuthorJSON
		if json.Unmarshal(items[i].UserInformation, &author) == nil && validMattermostID(author.ID) {
			authors[i] = &author
			all = append(all, author.ID)
		}
		var ids []string
		if json.Unmarshal(items[i].Participants, &ids) != nil {
			continue
		}
		for _, id := range ids {
			if validMattermostID(id) {
				participants[i] = append(participants[i], id)
				all = append(all, id)
			}
		}
	}
	allowed, err := c.reachableIDs(ctx, op, all)
	if err != nil {
		return nil, err
	}
	for i := range items {
		it := &items[i]
		head := heads[i]
		channelID := head.ChannelID
		if channelID == "" {
			channelID = it.ChannelID
		}
		if !validMattermostID(head.ID) || !validMattermostID(channelID) || reachable[channelID] == nil ||
			(it.ChannelID != "" && it.ChannelID != channelID) {
			continue
		}
		ids := []string{}
		for _, id := range participants[i] {
			if allowed[id] {
				ids = append(ids, id)
			}
		}
		thread := InsightThread{PostID: head.ID, ChannelID: channelID, ReplyCount: head.ReplyCount, ParticipantIDs: ids,
			ChannelName: cleanText(it.ChannelName.Value), ChannelDisplayName: cleanText(it.ChannelDisplayName.Value),
			Post: insightRootOf(items[i].Post, head.ID, channelID), UserInformation: insightAuthorOf(authors[i], allowed)}
		result.Threads = append(result.Threads, thread)
	}
	result.Count = len(result.Threads)
	return result, nil
}

// insightRootOf reads the root message of a ranked thread with the message entry. A deleted message, one that
// is not the thread's, and an answer that carries nothing beyond the thread's own IDs yield no entry.
func insightRootOf(raw json.RawMessage, postID, channelID string) *MessageEntry {
	var post postJSON
	if json.Unmarshal(raw, &post) != nil || post.ID != postID || post.DeleteAt > 0 ||
		(post.ChannelID != "" && post.ChannelID != channelID) ||
		(post.CreateAt <= 0 && post.Message == "" && post.UserID == "") {
		return nil
	}
	post.ChannelID = channelID
	entry := messageEntryOf(post)
	return &entry
}

// insightAuthorOf keeps the full user information only for a reachable user and the ID alone otherwise.
func insightAuthorOf(author *insightAuthorJSON, allowed map[string]bool) *InsightUser {
	if author == nil {
		return nil
	}
	if !allowed[author.ID] {
		return &InsightUser{ID: author.ID}
	}
	return &InsightUser{ID: author.ID, FirstName: cleanText(author.FirstName.Value),
		LastName: cleanText(author.LastName.Value), Nickname: cleanText(author.Nickname.Value),
		Username: cleanText(author.Username.Value), CreatedAt: msToRFC3339(int64(author.CreateAt)),
		LastPictureUpdate: int64(author.LastPictureUpdate)}
}

// reachableIDs returns the set of the given valid user IDs this connection may reach.
func (c *Client) reachableIDs(ctx context.Context, op string, ids []string) (map[string]bool, error) {
	allowed, err := c.verifyUsersScope(ctx, op, ids)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		set[id] = true
	}
	return set, nil
}
