package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

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
	maxLinkURL = 1024
)

const (
	linkRightSchema = `{"type":"string","enum":["inherit","password","public"]}`
)

// linkProperties are the members of one share link as returned; the password is never part of it.
const linkProperties = `"file_id":{"type":"integer"},"url":{"type":"string"},"right":` + linkRightSchema + `,` +
	`"valid_until":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"access_blocked":{"type":"boolean"},"can_download":{"type":"boolean"},"can_edit":{"type":"boolean"},` +
	`"can_comment":{"type":"boolean"},"can_see_info":{"type":"boolean"},"can_see_stats":{"type":"boolean"},` +
	`"can_request_access":{"type":"boolean"},"created_by":{"type":"integer"},"views":{"type":"integer"}`

var linkSchema = `{"type":"object","properties":{` + linkProperties +
	`},"required":["file_id","right"],"additionalProperties":false}`

var linkResultSchema = `{"type":"object","properties":{` +
	`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},` +
	`"status":{"type":"string","enum":["done","pending"]},"link":` + linkSchema + `},` +
	`"required":["drive_id","file_id"],"additionalProperties":false}`

// linkEntrySchema is one entry of links.list: its share link is link, so it has no sharelink member.
var linkEntrySchema = `{"type":"object","properties":{` + fileEntryProperties("sharelink") + `,"link":` +
	linkSchema + `},"required":["id","name","type","parent_id"],"additionalProperties":false}`

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
	{Name: "created_by", Description: "Identifier of the user who created the link; on links.list only with " +
		"sharelink or sharelink.views"},
	{Name: "views", Description: "Number of views of the last 180 days; only with with views (links.list: " +
		"sharelink.views)"},
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

var linksGet = capability.Descriptor{
	ID:      Provider + ".links.get",
	Version: 1,
	Title:   "Get the Infomaniak kDrive share link of a file or folder",
	Description: "Read the share link of exactly one file or folder of a drive this connection may reach; the URL " +
		"is access to the content and the password is never returned; a file without a link is not found",
	Tags:                  []string{"infomaniak", "kdrive", "links", "share", "get"},
	Risk:                  linkReadRisk,
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,` + withInputSchema(linkWithValues) + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(linkResultSchema),
	Arguments: []capability.Argument{driveIDArgument, linkFileArgument,
		{Name: "with", Description: "Related data to load; views adds the number of views of the last 180 days"}},
	Fields: linkResultFields,
	Examples: []capability.Example{{Description: "Read the share link of one file",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

var linksList = capability.Descriptor{
	ID:      Provider + ".links.list",
	Version: 1,
	Title:   "List the Infomaniak kDrive files that have a share link",
	Description: "List the files and folders of one drive this connection may reach that have a share link, one " +
		"page at a time with an opaque cursor; the URLs are access to the content and no password is returned",
	Tags:                  []string{"infomaniak", "kdrive", "links", "share", "list"},
	Risk:                  linkReadRisk,
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":` + strconv.Itoa(minListLimit) + `,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		listFilterInput(linksOrderByValues, true) + `,` + fileWithInput + `},"required":["drive_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drive_id":{"type":"integer"},"entries":{"type":"array","items":` + linkEntrySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"},` +
		`"response_at":{"type":"string"}},` +
		`"required":["drive_id","entries","has_more","count"],"additionalProperties":false}`),
	Arguments: slices.Concat([]capability.Argument{
		driveIDArgument,
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page, valid only with the same filter and sort arguments; omitted for the first page"},
		{Name: "limit", Description: "Entries per page, 5 to 1000; 10 when omitted"},
	}, listFilterArguments(true), []capability.Argument{
		{Name: "with", Description: "Related data to load per entry; each value adds the fields it documents, " +
			"and an entry's field exists only when its value is requested; sharelink adds created_by and " +
			"sharelink.views adds views to link"},
	}),
	Fields: append(slices.Concat(entryFields, fileWithFieldsWithout("sharelink")),
		capability.Field{Name: "link", Description: "The share link of the entry; untrusted data, see links.get; " +
			"created_by with sharelink, views with sharelink.views"},
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains, so this page is not every link"},
		capability.Field{Name: "count", Description: "Number of entries on this page"},
		responseAtField,
	),
	Examples: []capability.Example{{Description: "List the files with a share link in one drive",
		Arguments: json.RawMessage(`{"drive_id":1}`)}},
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
	CreatedBy    *int64 `json:"created_by"`
	CreatedAt    *int64 `json:"created_at"`
	UpdatedAt    *int64 `json:"updated_at"`
	Views        *int64 `json:"views"`
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
	// CreatedBy is the creator; links.list reports it only when its with values ask for the link's details.
	// Views exists only when views were asked for.
	CreatedBy int64  `json:"created_by,omitempty"`
	Views     *int64 `json:"views,omitempty"`
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
	// ResponseAt is the time Infomaniak generated the page.
	ResponseAt string `json:"response_at,omitempty"`
}

// linkWithViews is the one with value of links.get.
const linkWithViews = "views"

var linkWithValues = []string{linkWithViews}

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
	link := &Link{FileID: raw.FileID, URL: safeLinkURL(raw.URL), Right: raw.Right, CreatedBy: optionalID(raw.CreatedBy),
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

func invokeLinksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get share link"
	var input struct {
		fileArguments
		With []string `json:"with"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(linkFileReason)
	}
	if err := checkValues(input.With, linkWithValues, "with"); err != nil {
		return nil, err
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID, input.FileID)
	if err != nil {
		return nil, err
	}
	if err := client.refuseAncestor(ctx, op, input.DriveID, input.FileID); err != nil {
		return nil, err
	}
	return client.GetLink(ctx, input.DriveID, input.FileID, input.With)
}

// GetLink reads the share link of exactly one file or folder; with loads related data.
func (c *Client) GetLink(ctx context.Context, driveID, fileID int64, with []string) (*LinkResult, error) {
	const op = "get share link"
	if err := checkValues(with, linkWithValues, "with"); err != nil {
		return nil, err
	}
	var query url.Values
	if len(with) > 0 {
		query = url.Values{"with": {strings.Join(with, ",")}}
	}
	var raw shareLinkJSON
	if err := c.do(ctx, op, linkPath(driveID, fileID), query, &raw); err != nil {
		return nil, err
	}
	link, ok := linkOf(raw)
	if !ok || raw.FileID != fileID {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response")
	}
	if slices.Contains(with, linkWithViews) {
		link.Views = valuePointer(raw.Views)
	}
	return &LinkResult{DriveID: driveID, FileID: fileID, Link: link}, nil
}

func linkPath(driveID, fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/link", driveID, fileID)
}

type linksListArguments struct {
	DriveID int64    `json:"drive_id"`
	Cursor  string   `json:"cursor"`
	Limit   int      `json:"limit"`
	With    []string `json:"with"`
	listFilter
}

func invokeLinksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list share links"
	var input linksListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkValues(input.With, fileWithValues, "with"); err != nil {
		return nil, err
	}
	if err := input.validate(linksOrderByValues, true); err != nil {
		return nil, err
	}
	cursor, err := input.openCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := prepareWhole(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	page, err := client.listLinks(ctx, input.DriveID, cursor, limit, input.With, input.listFilter)
	if err != nil {
		return nil, err
	}
	page.Cursor = input.sealCursor(page.Cursor)
	return page, nil
}

// ListLinks reads one unfiltered page of the files that have a share link, cursor-paginated exactly as
// Infomaniak answers: it never follows has_more itself, and the cursor stays Infomaniak's own. with loads
// related data per entry; the link always comes with sharelink and is reported as link, never as sharelink.
func (c *Client) ListLinks(ctx context.Context, driveID int64, cursor string, limit int, with []string) (*LinkPage, error) {
	return c.listLinks(ctx, driveID, cursor, limit, with, listFilter{})
}

func (c *Client) listLinks(ctx context.Context, driveID int64, cursor string, limit int, with []string,
	filter listFilter) (*LinkPage, error) {
	const op = "list share links"
	if err := checkValues(with, fileWithValues, "with"); err != nil {
		return nil, err
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}, "with": {withQuery("sharelink", with)}}
	// On a drive with exclusions every row is proven by its own parents; the entry shows them only if asked.
	excluding := c.scope.hasFileExcludes(driveID)
	if excluding && !slices.Contains(with, "parents") {
		query.Set("with", query.Get("with")+",parents")
	}
	var ancestors map[int64]bool
	if excluding {
		var err error
		if ancestors, err = c.loadAncestors(ctx, op, driveID); err != nil {
			return nil, err
		}
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	filter.apply(query)
	var items []struct {
		fileJSON
		Sharelink *shareLinkJSON `json:"sharelink"`
	}
	var meta navigatorJSON
	if err := c.doInto(ctx, op, fmt.Sprintf("/3/drive/%d/files/links", driveID), query, &items, &meta); err != nil {
		return nil, err
	}
	entryWith := slices.DeleteFunc(slices.Clone(with), func(v string) bool { return strings.HasPrefix(v, "sharelink") })
	entries := make([]LinkEntry, 0, len(items))
	for _, item := range items {
		if excluding && !c.notExcluded(driveID, item.ID, &item.fileJSON) || ancestors[item.ID] {
			continue
		}
		entry := LinkEntry{Entry: c.entryWith(driveID, item.fileJSON, entryWith)}
		if item.Sharelink != nil && item.Sharelink.FileID == item.ID {
			if link, ok := linkOf(*item.Sharelink); ok {
				if !slices.Contains(with, "sharelink") && !slices.Contains(with, "sharelink.views") {
					link.CreatedBy = 0
				}
				if slices.Contains(with, "sharelink.views") {
					link.Views = valuePointer(item.Sharelink.Views)
				}
				entry.Link = link
			}
		}
		entries = append(entries, entry)
	}
	return &LinkPage{DriveID: driveID, Entries: entries, Cursor: meta.Cursor, HasMore: meta.HasMore,
		Count: len(entries), ResponseAt: positiveTime(meta.ResponseAt)}, nil
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
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID, input.FileID)
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
	return nil, "", env.unsuccessful(op,
		"Infomaniak reported an error for a response with an HTTP success status"+uncertain)
}
