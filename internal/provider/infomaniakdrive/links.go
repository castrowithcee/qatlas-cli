package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// linkSensitivity classifies a share link as its own class. A link URL is an access capability for the shared
// content, not metadata about it, so it never shares the class of file names and sizes.
const linkSensitivity = "infomaniak-kdrive-share-links"

// Share link rights, as the Infomaniak API defines them: inherit reaches only users of the drive, password
// is public but protected by a password, public has no restriction at all.
const (
	rightInherit  = "inherit"
	rightPassword = "password"
	rightPublic   = "public"
)

// Bounds of the arguments and of an answered link.
const (
	// minLinkPassword keeps a password long enough for the redactor to mask it; it ignores shorter values.
	minLinkPassword = 8
	maxLinkPassword = 128
	// maxLinkHorizon is how far ahead a link may be set to expire.
	maxLinkHorizon = 10 * 365 * 24 * time.Hour
	maxLinkURL     = 1024
)

// linkNow is the clock the expiry check reads; the package's own tests replace it.
var linkNow = time.Now

const (
	linkRightSchema    = `{"type":"string","enum":["inherit","password","public"]}`
	linkPasswordSchema = `{"type":"string","minLength":8,"maxLength":128}`
	linkExpirySchema   = `{"type":"string","minLength":20,"maxLength":35}`
	linkFlagSchema     = `{"type":"boolean"}`
)

// linkSchema is one share link as returned; the password is never part of it.
var linkSchema = `{"type":"object","properties":{` +
	`"file_id":{"type":"integer"},"url":{"type":"string"},"right":` + linkRightSchema + `,` +
	`"valid_until":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"access_blocked":{"type":"boolean"},"can_download":{"type":"boolean"},"can_edit":{"type":"boolean"},` +
	`"can_comment":{"type":"boolean"},"can_see_info":{"type":"boolean"},"can_see_stats":{"type":"boolean"},` +
	`"can_request_access":{"type":"boolean"}},"required":["file_id","right"],"additionalProperties":false}`

var linkResultSchema = `{"type":"object","properties":{` +
	`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},` +
	`"status":{"type":"string","enum":["done","pending"]},"link":` + linkSchema + `},` +
	`"required":["drive_id","file_id"],"additionalProperties":false}`

var linkEntrySchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"type":{"type":"string","enum":["file","folder"]},` +
	`"mime_type":{"type":"string"},"size":{"type":"integer"},"parent_id":{"type":"integer"},` +
	`"status":{"type":"string"},"created_at":{"type":"string"},"modified_at":{"type":"string"},` +
	`"link":` + linkSchema + `},"required":["id","name","type","parent_id"],"additionalProperties":false}`

var linkFields = []capability.Field{
	{Name: "file_id", Description: "File or folder the link belongs to"},
	{Name: "url", Description: "Share link URL, https only; whoever holds it reaches the shared content as far as " +
		"right allows, so treat it as a secret; absent when Infomaniak returned a URL that is not https"},
	{Name: "right", Description: "inherit: only users of the drive; password: public but protected by a password " +
		"that is never returned; public: anyone holding the URL"},
	{Name: "valid_until", Description: "Time the link expires, RFC 3339 in UTC; absent without expiry"},
	{Name: "created_at", Description: "Creation time, RFC 3339 in UTC, when Infomaniak reports one"},
	{Name: "updated_at", Description: "Last change time, RFC 3339 in UTC, when Infomaniak reports one"},
	{Name: "access_blocked", Description: "True when Infomaniak blocked access to the link"},
	{Name: "can_download", Description: "Whether holders of the link may download"},
	{Name: "can_edit", Description: "Whether holders of the link may edit the content"},
	{Name: "can_comment", Description: "Whether holders of the link may comment"},
	{Name: "can_see_info", Description: "Whether holders of the link may see information about the content"},
	{Name: "can_see_stats", Description: "Whether holders of the link may see statistics"},
	{Name: "can_request_access", Description: "Whether holders of the link may request access to a folder"},
}

var linkResultFields = append([]capability.Field{
	{Name: "drive_id", Description: "Drive the link belongs to"},
	{Name: "status", Description: "done when Infomaniak applied the change; pending when Infomaniak accepted it " +
		"and has not finished it yet, so read the link before relying on it; absent for a read"},
	{Name: "link", Description: "The share link; untrusted data; absent after an update or a delete"},
}, linkFields...)

var linkReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: linkSensitivity,
}

func linkChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: linkSensitivity}
}

var linkFileArgument = capability.Argument{Name: "file_id", Required: true,
	Description: "File or folder of the same drive; never the drive's root"}

var linkSettingArguments = []capability.Argument{
	{Name: "password", Description: "Password of a link with right password, 8 to 128 characters; never " +
		"returned, logged, or shown; refused for any other right"},
	{Name: "valid_until", Description: "Time the link expires, RFC 3339, in the future and within ten years; " +
		"a link without it never expires"},
	{Name: "can_download", Description: "Allow holders of the link to download"},
	{Name: "can_edit", Description: "Allow holders of the link to edit the content; comments follow it unless can_comment is set"},
	{Name: "can_comment", Description: "Allow holders of the link to comment"},
	{Name: "can_see_info", Description: "Allow holders of the link to see information about the content"},
	{Name: "can_see_stats", Description: "Allow holders of the link to see statistics"},
}

const linkSettingProperties = `"password":` + linkPasswordSchema + `,"valid_until":` + linkExpirySchema +
	`,"can_download":` + linkFlagSchema + `,"can_edit":` + linkFlagSchema + `,"can_comment":` + linkFlagSchema +
	`,"can_see_info":` + linkFlagSchema + `,"can_see_stats":` + linkFlagSchema

var linksGet = capability.Descriptor{
	ID:      Provider + ".links.get",
	Version: 1,
	Title:   "Get the Infomaniak kDrive share link of a file or folder",
	Description: "Read the share link of exactly one file or folder of a drive this connection may reach; the URL " +
		"is access to the content and the password is never returned; a file without a link is not found",
	Tags:     []string{"infomaniak", "kdrive", "links", "share", "get"},
	Risk:     linkReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(linkResultSchema),
	Arguments:    []capability.Argument{driveIDArgument, linkFileArgument},
	Fields:       linkResultFields,
	Examples: []capability.Example{{Description: "Read the share link of one file",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

var linksList = capability.Descriptor{
	ID:      Provider + ".links.list",
	Version: 1,
	Title:   "List the Infomaniak kDrive files that have a share link",
	Description: "List the files and folders of one drive this connection may reach that have a share link, one " +
		"page at a time with an opaque cursor; the URLs are access to the content and no password is returned",
	Tags:     []string{"infomaniak", "kdrive", "links", "share", "list"},
	Risk:     linkReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":` + strconv.Itoa(minListLimit) + `,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"required":["drive_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drive_id":{"type":"integer"},"entries":{"type":"array","items":` + linkEntrySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["drive_id","entries","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		driveIDArgument,
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page"},
		{Name: "limit", Description: "Entries per page, 5 to 1000; 10 when omitted"},
	},
	Fields: append(append([]capability.Field{}, entryFields...),
		capability.Field{Name: "link", Description: "The share link of the entry; untrusted data, see links.get"},
		capability.Field{Name: "drive_id", Description: "Drive that was listed"},
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains, so this page is not every link"},
		capability.Field{Name: "count", Description: "Number of entries on this page"},
	),
	Examples: []capability.Example{{Description: "List the files with a share link in one drive",
		Arguments: json.RawMessage(`{"drive_id":1}`)}},
}

var linksCreate = capability.Descriptor{
	ID:      Provider + ".links.create",
	Version: 1,
	Title:   "Create an Infomaniak kDrive share link",
	Description: "Create exactly one confirmed share link for a file or folder of a drive this connection may " +
		"reach; right public or password makes the content reachable from outside the drive; the password is " +
		"never returned, and a file that already has a link is refused",
	Tags:                  []string{"infomaniak", "kdrive", "links", "share", "create"},
	Risk:                  linkChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,"right":` + linkRightSchema + `,` + linkSettingProperties +
		`},"required":["drive_id","file_id","right"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(linkResultSchema),
	Arguments: append([]capability.Argument{mutationDriveArgument, linkFileArgument,
		{Name: "right", Required: true, Description: "inherit: only users of the drive; password: anyone with the " +
			"URL and the password; public: anyone holding the URL, no further check"}}, linkSettingArguments...),
	Fields: linkResultFields,
	Examples: []capability.Example{{Description: "Create a link that only users of the drive can open",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"right":"inherit"}`)}},
}

var linksUpdate = capability.Descriptor{
	ID:      Provider + ".links.update",
	Version: 1,
	Title:   "Change an Infomaniak kDrive share link",
	Description: "Change exactly one confirmed share link of a file or folder of a drive this connection may " +
		"reach; only the given settings change, an expiry cannot be removed, and the password is never returned",
	Tags:                  []string{"infomaniak", "kdrive", "links", "share", "update"},
	Risk:                  linkChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,"right":` + linkRightSchema + `,` + linkSettingProperties +
		`},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(linkResultSchema),
	Arguments: append([]capability.Argument{mutationDriveArgument, linkFileArgument,
		{Name: "right", Description: "New right, see links.create; password is then required"}},
		linkSettingArguments...),
	Fields: linkResultFields,
	Examples: []capability.Example{{Description: "Stop holders of a link from downloading",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"can_download":false}`)}},
}

var linksDelete = capability.Descriptor{
	ID:      Provider + ".links.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kDrive share link",
	Description: "Delete exactly one confirmed share link of a file or folder of a drive this connection may " +
		"reach; the file itself stays, and the old URL stops working",
	Tags:                  []string{"infomaniak", "kdrive", "links", "share", "delete"},
	Risk:                  linkChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(linkResultSchema),
	Arguments:    []capability.Argument{mutationDriveArgument, linkFileArgument},
	Fields:       linkResultFields,
	Examples: []capability.Example{{Description: "Delete the share link of one file",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

// shareLinkJSON is the subset of the Infomaniak ShareLink resource this provider reads. It deliberately has
// no password field: whatever Infomaniak sends about one is never decoded.
type shareLinkJSON struct {
	URL          string `json:"url"`
	FileID       int64  `json:"file_id"`
	Right        string `json:"right"`
	ValidUntil   *int64 `json:"valid_until"`
	CreatedAt    *int64 `json:"created_at"`
	UpdatedAt    *int64 `json:"updated_at"`
	Capabilities struct {
		CanEdit          bool `json:"can_edit"`
		CanSeeStats      bool `json:"can_see_stats"`
		CanSeeInfo       bool `json:"can_see_info"`
		CanDownload      bool `json:"can_download"`
		CanComment       bool `json:"can_comment"`
		CanRequestAccess bool `json:"can_request_access"`
	} `json:"capabilities"`
	AccessBlocked bool `json:"access_blocked"`
}

// Link is the stable Qatlas view of one share link. It has no password.
type Link struct {
	FileID           int64  `json:"file_id"`
	URL              string `json:"url,omitempty"`
	Right            string `json:"right"`
	ValidUntil       string `json:"valid_until,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	AccessBlocked    bool   `json:"access_blocked"`
	CanDownload      bool   `json:"can_download"`
	CanEdit          bool   `json:"can_edit"`
	CanComment       bool   `json:"can_comment"`
	CanSeeInfo       bool   `json:"can_see_info"`
	CanSeeStats      bool   `json:"can_see_stats"`
	CanRequestAccess bool   `json:"can_request_access"`
}

// LinkResult is the stable answer of a link read or change.
type LinkResult struct {
	DriveID int64  `json:"drive_id"`
	FileID  int64  `json:"file_id"`
	Status  string `json:"status,omitempty"`
	Link    *Link  `json:"link,omitempty"`
}

// LinkEntry is one file or folder of a link listing.
type LinkEntry struct {
	Entry
	Link *Link `json:"link,omitempty"`
}

// LinkPage is one paginated listing of the files that have a share link.
type LinkPage struct {
	DriveID int64       `json:"drive_id"`
	Entries []LinkEntry `json:"entries"`
	Cursor  string      `json:"cursor,omitempty"`
	HasMore bool        `json:"has_more"`
	Count   int         `json:"count"`
}

func validRight(right string) bool {
	return right == rightInherit || right == rightPassword || right == rightPublic
}

// safeLinkURL keeps only an absolute https URL without credentials that fits the bound. Anything else,
// including a truncated one, is dropped rather than returned.
func safeLinkURL(raw string) string {
	if raw == "" || len(raw) > maxLinkURL {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return raw
}

// linkOf normalises one answered link. It reports false for a right outside the documented set.
func linkOf(raw shareLinkJSON) (*Link, bool) {
	if !validRight(raw.Right) {
		return nil, false
	}
	link := &Link{FileID: raw.FileID, URL: safeLinkURL(raw.URL), Right: raw.Right,
		AccessBlocked: raw.AccessBlocked, CanDownload: raw.Capabilities.CanDownload, CanEdit: raw.Capabilities.CanEdit,
		CanComment: raw.Capabilities.CanComment, CanSeeInfo: raw.Capabilities.CanSeeInfo,
		CanSeeStats: raw.Capabilities.CanSeeStats, CanRequestAccess: raw.Capabilities.CanRequestAccess}
	if raw.ValidUntil != nil && *raw.ValidUntil > 0 {
		link.ValidUntil = timeOf(*raw.ValidUntil)
	}
	if raw.CreatedAt != nil && *raw.CreatedAt > 0 {
		link.CreatedAt = timeOf(*raw.CreatedAt)
	}
	if raw.UpdatedAt != nil && *raw.UpdatedAt > 0 {
		link.UpdatedAt = timeOf(*raw.UpdatedAt)
	}
	return link, true
}

const linkFileReason = "file_id must be a positive integer other than the drive's root"

type linkArguments struct {
	DriveID     int64  `json:"drive_id"`
	FileID      int64  `json:"file_id"`
	Right       string `json:"right"`
	Password    string `json:"password"`
	ValidUntil  string `json:"valid_until"`
	CanDownload *bool  `json:"can_download"`
	CanEdit     *bool  `json:"can_edit"`
	CanComment  *bool  `json:"can_comment"`
	CanSeeInfo  *bool  `json:"can_see_info"`
	CanSeeStats *bool  `json:"can_see_stats"`
}

// validLinkPassword accepts printable text of the bounded length; the value is never quoted in a refusal.
func validLinkPassword(password string) bool {
	if len(password) < minLinkPassword || len(password) > maxLinkPassword || !utf8.ValidString(password) {
		return false
	}
	for _, r := range password {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// linkBody checks the settings of a create or an update and returns the one body they allow: only the
// documented fields, only when given. No value is ever quoted in a refusal.
func linkBody(input linkArguments, create bool) (map[string]any, error) {
	body := map[string]any{}
	switch {
	case create && input.Right == "":
		return nil, invalidRequest("right is required")
	case input.Right != "" && !validRight(input.Right):
		return nil, invalidRequest("right must be inherit, password, or public")
	}
	if input.Right != "" {
		body["right"] = input.Right
	}
	switch {
	case input.Right == rightPassword && input.Password == "":
		return nil, invalidRequest("a link with right password needs a password")
	case input.Password != "" && input.Right != rightPassword:
		return nil, invalidRequest("a password is only allowed together with right password")
	case input.Password != "" && !validLinkPassword(input.Password):
		return nil, invalidRequest("password must be 8 to 128 characters without a control character")
	}
	if input.Password != "" {
		body["password"] = input.Password
	}
	if input.ValidUntil != "" {
		at, err := time.Parse(time.RFC3339, input.ValidUntil)
		now := linkNow()
		if err != nil || !at.After(now) || at.After(now.Add(maxLinkHorizon)) {
			return nil, invalidRequest("valid_until must be an RFC 3339 time in the future, at most ten years ahead")
		}
		body["valid_until"] = at.Unix()
	}
	for name, flag := range map[string]*bool{"can_download": input.CanDownload, "can_edit": input.CanEdit,
		"can_comment": input.CanComment, "can_see_info": input.CanSeeInfo, "can_see_stats": input.CanSeeStats} {
		if flag != nil {
			body[name] = *flag
		}
	}
	if len(body) == 0 {
		return nil, invalidRequest("an update needs at least one setting to change")
	}
	return body, nil
}

func invokeLinksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get share link"
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(linkFileReason)
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.GetLink(ctx, input.DriveID, input.FileID)
}

// GetLink reads the share link of exactly one file or folder.
func (c *Client) GetLink(ctx context.Context, driveID, fileID int64) (*LinkResult, error) {
	const op = "get share link"
	var raw shareLinkJSON
	if err := c.do(ctx, op, linkPath(driveID, fileID), nil, &raw); err != nil {
		return nil, err
	}
	link, ok := linkOf(raw)
	if !ok || raw.FileID != fileID {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response")
	}
	return &LinkResult{DriveID: driveID, FileID: fileID, Link: link}, nil
}

func linkPath(driveID, fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/link", driveID, fileID)
}

type linksListArguments struct {
	DriveID int64  `json:"drive_id"`
	Cursor  string `json:"cursor"`
	Limit   int    `json:"limit"`
}

func invokeLinksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list share links"
	var input linksListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.ListLinks(ctx, input.DriveID, input.Cursor, limit)
}

// ListLinks reads one page of the files that have a share link, cursor-paginated exactly as Infomaniak
// answers: it never follows has_more itself.
func (c *Client) ListLinks(ctx context.Context, driveID int64, cursor string, limit int) (*LinkPage, error) {
	const op = "list share links"
	query := url.Values{"limit": {strconv.Itoa(limit)}, "with": {"sharelink"}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var items []struct {
		fileJSON
		Sharelink *shareLinkJSON `json:"sharelink"`
	}
	var meta navigatorJSON
	if err := c.doInto(ctx, op, fmt.Sprintf("/3/drive/%d/files/links", driveID), query, &items, &meta); err != nil {
		return nil, err
	}
	entries := make([]LinkEntry, 0, len(items))
	for _, item := range items {
		entry := LinkEntry{Entry: entryOf(item.fileJSON)}
		if item.Sharelink != nil && item.Sharelink.FileID == item.ID {
			if link, ok := linkOf(*item.Sharelink); ok {
				entry.Link = link
			}
		}
		entries = append(entries, entry)
	}
	return &LinkPage{DriveID: driveID, Entries: entries, Cursor: meta.Cursor, HasMore: meta.HasMore,
		Count: len(entries)}, nil
}

func invokeLinksCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeLinkChange(ctx, resolved, secrets, red, raw, "create share link", true)
}

func invokeLinksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeLinkChange(ctx, resolved, secrets, red, raw, "update share link", false)
}

// invokeLinkChange runs the local checks of a create or an update in order: the password joins the redactor
// first, so no later step can show it, then the identifiers and settings, then the drive checks, and only
// then the one request.
func invokeLinkChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string, create bool) (any, error) {
	var input linkArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if red != nil && input.Password != "" {
		red.Add(input.Password)
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(linkFileReason)
	}
	body, err := linkBody(input, create)
	if err != nil {
		return nil, err
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	if create {
		return client.CreateLink(ctx, input.DriveID, input.FileID, body)
	}
	return client.UpdateLink(ctx, input.DriveID, input.FileID, body)
}

// CreateLink creates exactly one share link, once. body holds the settings linkBody validated.
func (c *Client) CreateLink(ctx context.Context, driveID, fileID int64, body map[string]any) (*LinkResult, error) {
	const op = "create share link"
	env, status, err := c.linkRequest(ctx, op, http.MethodPost, linkPath(driveID, fileID), body)
	if err != nil {
		return nil, err
	}
	result := &LinkResult{DriveID: driveID, FileID: fileID, Status: status}
	var raw shareLinkJSON
	if json.Unmarshal(env.Data, &raw) == nil {
		if link, ok := linkOf(raw); ok && raw.FileID == fileID {
			result.Link = link
		}
	}
	if result.Link == nil && status == statusDone {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+uncertain)
	}
	return result, nil
}

// UpdateLink changes exactly one share link, once. Infomaniak answers a bare flag, so no link is returned.
func (c *Client) UpdateLink(ctx context.Context, driveID, fileID int64, body map[string]any) (*LinkResult, error) {
	const op = "update share link"
	_, status, err := c.linkRequest(ctx, op, http.MethodPut, linkPath(driveID, fileID), body)
	if err != nil {
		return nil, err
	}
	return &LinkResult{DriveID: driveID, FileID: fileID, Status: status}, nil
}

func invokeLinksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete share link"
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(linkFileReason)
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.DeleteLink(ctx, input.DriveID, input.FileID)
}

// DeleteLink deletes exactly one share link, once.
func (c *Client) DeleteLink(ctx context.Context, driveID, fileID int64) (*LinkResult, error) {
	const op = "delete share link"
	_, status, err := c.linkRequest(ctx, op, http.MethodDelete, linkPath(driveID, fileID), nil)
	if err != nil {
		return nil, err
	}
	return &LinkResult{DriveID: driveID, FileID: fileID, Status: status}, nil
}

// linkRequest sends exactly one change and maps its envelope to done or pending; anything else is an unclear
// outcome. The method is fixed by the caller, never by an agent.
func (c *Client) linkRequest(ctx context.Context, op, method, path string, body any) (*envelope, string, error) {
	env, _, err := c.request(ctx, op, method, path, nil, body, true)
	if err != nil {
		return nil, "", err
	}
	switch env.Result {
	case "success":
		return env, statusDone, nil
	case "asynchronous":
		return env, statusPending, nil
	}
	return nil, "", invalidResponse(op, "Infomaniak reported an error for a response with an HTTP success status"+uncertain)
}
