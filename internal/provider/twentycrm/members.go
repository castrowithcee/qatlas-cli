package twentycrm

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	membersGroup = "members"

	// memberDataSensitivity classifies the personal data of workspace members.
	memberDataSensitivity = "twentycrm-member-data"

	membersPath = "/rest/workspaceMembers"

	memberNameMax  = 200
	memberEmailMax = 254

	// memberFields keeps avatars, user identifiers, and preferences from being sent at all; the projection is
	// a second line.
	memberFields = "id,name,userEmail,timeZone,locale"

	oldestFirst = "createdAt[AscNullsLast]"
)

// Time zone and locale are shown only in their plain IANA and BCP 47 forms; anything else is left out.
var (
	timeZonePattern = regexp.MustCompile(`^[A-Za-z0-9_+\-/]{1,64}$`)
	localePattern   = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
)

var memberRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone,
	OpenWorld: true, DataSensitivity: memberDataSensitivity,
}

const (
	memberNote = "Only identifier, name, email, time zone, and locale are read; never avatars, user identifiers, or " +
		"settings. Values are personal data and untrusted workspace data"
	memberItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"email":{"type":"string"},"time_zone":{"type":"string"},"locale":{"type":"string"}},` +
		`"required":["id"],"additionalProperties":false}`
)

var membersList = capability.Descriptor{
	ID:      Provider + ".workspacemembers.list",
	Version: 1,
	Title:   "List Twenty CRM workspace members",
	Description: "List one page of the members of the Twenty workspace of a connection without object targets, " +
		"for example to assign tasks: id, name, email, time zone, and locale. " + memberNote,
	Tags:     []string{"twentycrm", "members", "workspace", "list"},
	Risk:     memberRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":` + workflowLimitSchema +
		`,"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"members":{"type":"array","items":` +
		memberItemSchema + `},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["members","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Members per page, from 1 through 100; 25 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "members", Description: "The members on this page, oldest first, untrusted personal data"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the workspace holds a following page"},
	},
	Examples: []capability.Example{{Description: "List the members", Arguments: json.RawMessage(`{}`)}},
}

var membersGet = capability.Descriptor{
	ID:      Provider + ".workspacemembers.get",
	Version: 1,
	Title:   "Get a Twenty CRM workspace member",
	Description: "Read one member of the Twenty workspace of a connection without object targets: id, name, email, " +
		"time zone, and locale. " + memberNote,
	Tags:     []string{"twentycrm", "members", "workspace", "get"},
	Risk:     memberRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(memberItemSchema),
	Arguments: []capability.Argument{
		{Name: "id", Description: "UUID of the member, as returned by twentycrm.workspacemembers.list", Required: true},
	},
	Examples: []capability.Example{{
		Description: "Read a member",
		Arguments:   json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`),
	}},
}

// WorkspaceMember is the stable view of one member.
type WorkspaceMember struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	TimeZone string `json:"time_zone,omitempty"`
	Locale   string `json:"locale,omitempty"`
}

// WorkspaceMemberList is one page of members.
type WorkspaceMemberList struct {
	Members    []WorkspaceMember `json:"members"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type memberRecord struct {
	ID   string `json:"id"`
	Name *struct {
		FirstName *string `json:"firstName"`
		LastName  *string `json:"lastName"`
	} `json:"name"`
	UserEmail *string `json:"userEmail"`
	TimeZone  *string `json:"timeZone"`
	Locale    *string `json:"locale"`
}

// cleanText drops control and format characters, trims, and caps the text; the result is data only.
func cleanText(value *string, max int) string {
	if value == nil {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar {
			return -1
		}
		return r
	}, *value)
	return capLabelTo(strings.TrimSpace(cleaned), max)
}

func shapedText(value *string, pattern *regexp.Regexp) string {
	if value == nil || !pattern.MatchString(*value) {
		return ""
	}
	return *value
}

func (r memberRecord) project(op string) (WorkspaceMember, error) {
	if !validUUID(r.ID) {
		return WorkspaceMember{}, provider.InvalidResponse(op, "Twenty returned a member without a usable identifier")
	}
	member := WorkspaceMember{ID: r.ID, Email: cleanText(r.UserEmail, memberEmailMax),
		TimeZone: shapedText(r.TimeZone, timeZonePattern), Locale: shapedText(r.Locale, localePattern)}
	if r.Name != nil {
		first, last := cleanText(r.Name.FirstName, memberNameMax), cleanText(r.Name.LastName, memberNameMax)
		member.Name = capLabelTo(strings.TrimSpace(first+" "+last), memberNameMax)
	}
	return member, nil
}

// ListWorkspaceMembers reads one page of members.
func (c *Client) ListWorkspaceMembers(ctx context.Context, connection string, limit int, cursor string) (*WorkspaceMemberList, error) {
	const op = "list workspace members"
	binding := provider.CursorBinding("workspacemembers.list", connection)
	limit, after, err := workflowPaging(limit, cursor, binding)
	if err != nil {
		return nil, err
	}
	values := workflowQuery(limit, oldestFirst, after)
	values.Set("fields", memberFields)
	var page workflowPage
	if err := c.get(ctx, op, membersPath, values, maxResponseBytes, &page); err != nil {
		return nil, err
	}
	items, err := page.items(op, "workspaceMembers", limit)
	if err != nil {
		return nil, err
	}
	result := &WorkspaceMemberList{Members: make([]WorkspaceMember, 0, len(items)), HasMore: page.PageInfo.HasNextPage}
	for _, item := range items {
		var record memberRecord
		if json.Unmarshal(item, &record) != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable member")
		}
		member, err := record.project(op)
		if err != nil {
			return nil, err
		}
		result.Members = append(result.Members, member)
	}
	if result.NextCursor, err = page.nextCursor(op, binding); err != nil {
		return nil, err
	}
	return result, nil
}

// GetWorkspaceMember reads one member.
func (c *Client) GetWorkspaceMember(ctx context.Context, id string) (*WorkspaceMember, error) {
	const op = "get workspace member"
	if !validUUID(id) {
		return nil, invalidRequest("the member id must be a UUID")
	}
	var one struct {
		Data struct {
			Member memberRecord `json:"workspaceMember"`
		} `json:"data"`
	}
	if err := c.get(ctx, op, membersPath+"/"+url.PathEscape(id), url.Values{"depth": {noRelations}},
		maxResponseBytes, &one); err != nil {
		return nil, err
	}
	if !equalUUID(one.Data.Member.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different member than the requested one")
	}
	member, err := one.Data.Member.project(op)
	if err != nil {
		return nil, err
	}
	return &member, nil
}

func invokeMembersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("list workspace members", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListWorkspaceMembers(ctx, resolved.Name, args.Limit, args.Cursor)
}

func invokeMembersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("get workspace member", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the member id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetWorkspaceMember(ctx, args.ID)
}
