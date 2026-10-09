package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The shares of an identity and the people it can share with are personal data about who reaches what,
// not metadata about a file; the recipient directory is a different class again.
const (
	sharesSensitivity = "nextcloud-shares"
	shareeSensitivity = "nextcloud-directory"
)

// Bounds of the sharing reads.
const (
	maxShareEntries  = 500
	maxShareeEntries = 200
	maxShareIDLength = 18
	maxSearchLength  = 128
	maxPerPage       = 50
	defaultPerPage   = 10
	maxExpiryLength  = 64
)

// messageShareNotFound answers a share that does not exist and one that lies outside the connection
// alike, so the answer reveals nothing about the other.
const messageShareNotFound = "this Nextcloud connection does not hold this share"

// The directions of a share relative to the identity.
const (
	directionOutgoing = "outgoing"
	directionIncoming = "incoming"
)

// Bits of the permissions Nextcloud reports for a share.
const (
	permRead   = 1
	permUpdate = 2
	permCreate = 4
	permDelete = 8
	permShare  = 16
)

// shareTypeNames maps the numeric share type of the OCS API to the name Qatlas reports. A remote group
// (9) is a federated share as well; every other type, such as guests, Deck, or ScienceMesh, is "other".
var shareTypeNames = map[int64]string{
	0: "user", 1: "group", 3: "link", 4: "email", 6: "federated", 7: "team", 9: "federated", 10: "talk",
}

const shareTypeOther = "other"

func shareTypeName(raw json.Number) string {
	value, err := raw.Int64()
	if err != nil {
		return shareTypeOther
	}
	if name, ok := shareTypeNames[value]; ok {
		return name
	}
	return shareTypeOther
}

// Rights are the permissions of a share as flags.
type Rights struct {
	Read   bool `json:"read"`
	Update bool `json:"update"`
	Create bool `json:"create"`
	Delete bool `json:"delete"`
	Share  bool `json:"share"`
}

// Recipient is who a share reaches, as the instance names it.
type Recipient struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Share is the stable Qatlas view of one share. It carries no token, no link URL, and no password.
type Share struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Direction   string     `json:"direction"`
	Path        string     `json:"path"`
	ItemType    string     `json:"item_type,omitempty"`
	Recipient   *Recipient `json:"recipient,omitempty"`
	Rights      Rights     `json:"rights"`
	ExpiresAt   string     `json:"expires_at,omitempty"`
	Note        string     `json:"note,omitempty"`
	Label       string     `json:"label,omitempty"`
	HasPassword bool       `json:"has_password"`
}

// SharesResult is the normalised list of the shares below the connection root.
type SharesResult struct {
	Shares    []Share `json:"shares"`
	Count     int     `json:"count"`
	Truncated bool    `json:"truncated,omitempty"`
}

// Sharee is one possible recipient the instance knows.
type Sharee struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ShareeResult is the normalised recipient search.
type ShareeResult struct {
	Sharees   []Sharee `json:"sharees"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated,omitempty"`
}

// flexString reads a value that Nextcloud writes as a string or as a number.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*f = flexString(value)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*f = flexString(number.String())
	return nil
}

// rawShare lists only the members this adapter reads. The token, the URL, and the password hash of a link
// are deliberately not members: they cannot be decoded, so they cannot reach a result. For a link,
// Nextcloud puts the password hash into share_with, so that member is never reported for one.
type rawShare struct {
	ID           flexString  `json:"id"`
	ShareType    json.Number `json:"share_type"`
	Permissions  json.Number `json:"permissions"`
	UIDOwner     string      `json:"uid_owner"`
	UIDInitiator string      `json:"uid_initiator"`
	Path         string      `json:"path"`
	FileTarget   string      `json:"file_target"`
	ItemType     string      `json:"item_type"`
	ShareWith    string      `json:"share_with"`
	DisplayName  string      `json:"share_with_displayname"`
	Expiration   string      `json:"expiration"`
	Note         string      `json:"note"`
	Label        string      `json:"label"`
	Password     string      `json:"password"`
}

// below reports whether the absolute path of an identity's Files tree lies at or below the connection
// root and returns it relative to the root. A path with an empty, relative, or backslash component is never
// below the root.
func (c *Client) below(absolute string) (string, bool) {
	if !strings.HasPrefix(absolute, "/") {
		return "", false
	}
	trimmed := strings.TrimSuffix(absolute, "/")
	var segments []string
	if trimmed != "" {
		segments = strings.Split(trimmed[1:], "/")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.Contains(segment, `\`) {
			return "", false
		}
	}
	if len(segments) < len(c.root) || !equalSegments(segments[:len(c.root)], c.root) {
		return "", false
	}
	return strings.Join(segments[len(c.root):], "/"), true
}

// absolute turns a path relative to the root into the absolute path of the identity's Files tree.
func (c *Client) absolute(rel []string) string {
	return "/" + strings.Join(append(append([]string{}, c.root...), rel...), "/")
}

// normalise decides whether a share belongs to this connection and builds its view. An own share is bound by
// its path, an incoming one by its target in the tree of the identity. A share of another owner that the
// identity itself passed on is neither: its target lies in the tree of a recipient.
func (c *Client) normalise(raw rawShare) (Share, bool) {
	var direction, location string
	switch {
	case raw.UIDOwner == "":
		return Share{}, false
	case raw.UIDOwner == c.user:
		direction, location = directionOutgoing, raw.Path
	case raw.UIDInitiator == c.user:
		return Share{}, false
	default:
		direction, location = directionIncoming, raw.FileTarget
	}
	rel, ok := c.below(location)
	if !ok || raw.ID == "" {
		return Share{}, false
	}
	kind := shareTypeName(raw.ShareType)
	rights, _ := raw.Permissions.Int64()
	share := Share{
		ID: bounded(string(raw.ID)), Type: kind, Direction: direction, Path: rel,
		Rights: Rights{
			Read: rights&permRead != 0, Update: rights&permUpdate != 0, Create: rights&permCreate != 0,
			Delete: rights&permDelete != 0, Share: rights&permShare != 0,
		},
		ExpiresAt: expiry(raw.Expiration), Note: bounded(raw.Note), Label: bounded(raw.Label),
		HasPassword: raw.Password != "" || kind == "link" && raw.ShareWith != "",
	}
	if raw.ItemType == "file" || raw.ItemType == "folder" {
		share.ItemType = raw.ItemType
	}
	if kind != "link" && raw.ShareWith != "" {
		share.Recipient = &Recipient{ID: bounded(raw.ShareWith), Name: bounded(raw.DisplayName)}
	}
	return share, true
}

// expiry normalises the expiration Nextcloud writes without a zone to RFC 3339 in UTC; a value in another
// form is passed on bounded.
func expiry(raw string) string {
	if raw == "" {
		return ""
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.UTC); err == nil {
		return parsed.Format(time.RFC3339)
	}
	if len(raw) > maxExpiryLength {
		return raw[:maxExpiryLength]
	}
	return raw
}

func checkShareID(id string) error {
	if id == "" || len(id) > maxShareIDLength || !digitsOnly(id) {
		return providerError("get share", "a share ID is a number")
	}
	return nil
}

// ListShares reads the shares of the identity (or, with incoming, the shares made to it) and keeps those
// whose path lies below the root. A path narrows the request to that node, or with subfiles to the
// items of that folder; the bound is applied to every answered share regardless.
func (c *Client) ListShares(ctx context.Context, path string, subfiles, incoming bool) (*SharesResult, error) {
	const op = "list shares"
	query := url.Values{}
	if path != "" {
		rel, err := splitRelative(path)
		if err != nil {
			return nil, providerError(op, err.Error())
		}
		query.Set("path", c.absolute(rel))
	}
	if subfiles {
		if path == "" || incoming {
			return nil, providerError(op, "subfiles needs a path and cannot be combined with shared_with_me")
		}
		query.Set("subfiles", "true")
	}
	if incoming {
		query.Set("shared_with_me", "true")
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsSharing, suffix: []string{"shares"}, query: query})
	if err != nil {
		return nil, err
	}
	var items []rawShare
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud share list could not be read")
	}
	result := &SharesResult{Shares: []Share{}}
	want := directionOutgoing
	if incoming {
		want = directionIncoming
	}
	for _, item := range items {
		share, ok := c.normalise(item)
		if !ok || share.Direction != want {
			continue
		}
		if len(result.Shares) == maxShareEntries {
			result.Truncated = true
			break
		}
		result.Shares = append(result.Shares, share)
	}
	result.Count = len(result.Shares)
	return result, nil
}

// GetShare reads one share by its ID. A share outside the connection root is answered exactly like one that
// does not exist.
func (c *Client) GetShare(ctx context.Context, id string) (*Share, error) {
	const op = "get share"
	if err := checkShareID(id); err != nil {
		return nil, err
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsSharing, suffix: []string{"shares", id}})
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) && providerErr.Message == messageNotFound {
			return nil, shareNotFound(op)
		}
		return nil, err
	}
	var items []rawShare
	if err := json.Unmarshal(data, &items); err != nil || len(items) > 1 {
		return nil, invalidResponse(op, "the Nextcloud share could not be read")
	}
	if len(items) == 0 {
		return nil, shareNotFound(op)
	}
	share, ok := c.normalise(items[0])
	if !ok {
		return nil, shareNotFound(op)
	}
	return &share, nil
}

func shareNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageShareNotFound}
}

// rawSharee is one candidate of the recipient search.
type rawSharee struct {
	Label string `json:"label"`
	Value struct {
		ShareType json.Number `json:"shareType"`
		ShareWith string      `json:"shareWith"`
	} `json:"value"`
}

// shareeBuckets are the lists of the recipient answer this adapter reads, besides the same lists below
// "exact". The "lookup" list is never read: the global lookup server is not asked.
var shareeBuckets = []string{"users", "groups", "remotes", "remote_groups", "emails", "circles", "rooms"}

// SearchSharees searches the possible recipients of the instance. The search is instance-wide and
// independent of the root folder; the global lookup server is switched off.
func (c *Client) SearchSharees(ctx context.Context, search, itemType string, perPage int) (*ShareeResult, error) {
	const op = "search sharees"
	if search == "" || len(search) > maxSearchLength || perPage < 1 || perPage > maxPerPage ||
		itemType != "file" && itemType != "folder" {
		return nil, providerError(op, "the search arguments are unusable")
	}
	query := url.Values{
		"search": {search}, "itemType": {itemType}, "perPage": {strconv.Itoa(perPage)}, "lookup": {"false"},
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsSharing, suffix: []string{"sharees"}, query: query})
	if err != nil {
		return nil, err
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil, invalidResponse(op, "the Nextcloud recipient list could not be read")
	}
	buckets := []map[string]json.RawMessage{groups}
	if exact, ok := groups["exact"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(exact, &inner); err != nil {
			return nil, invalidResponse(op, "the Nextcloud recipient list could not be read")
		}
		buckets = append([]map[string]json.RawMessage{inner}, buckets...)
	}
	result := &ShareeResult{Sharees: []Sharee{}}
	seen := map[string]bool{}
	for _, bucket := range buckets {
		for _, name := range shareeBuckets {
			rawList, ok := bucket[name]
			if !ok {
				continue
			}
			var candidates []rawSharee
			if err := json.Unmarshal(rawList, &candidates); err != nil {
				return nil, invalidResponse(op, "the Nextcloud recipient list could not be read")
			}
			for _, candidate := range candidates {
				kind := shareTypeName(candidate.Value.ShareType)
				key := kind + "\x00" + candidate.Value.ShareWith
				if candidate.Value.ShareWith == "" || seen[key] {
					continue
				}
				if len(result.Sharees) == maxShareeEntries {
					result.Truncated = true
					continue
				}
				seen[key] = true
				result.Sharees = append(result.Sharees, Sharee{
					Type: kind, ID: bounded(candidate.Value.ShareWith), Name: bounded(candidate.Label),
				})
			}
		}
	}
	result.Count = len(result.Sharees)
	return result, nil
}

const (
	rightsSchema = `{"type":"object","properties":{"read":{"type":"boolean"},"update":{"type":"boolean"},` +
		`"create":{"type":"boolean"},"delete":{"type":"boolean"},"share":{"type":"boolean"}},` +
		`"required":["read","update","create","delete","share"],"additionalProperties":false}`
	shareSchema = `{"type":"object","properties":{"id":{"type":"string"},` +
		`"type":{"type":"string","enum":["user","group","link","email","federated","team","talk","other"]},` +
		`"direction":{"type":"string","enum":["outgoing","incoming"]},"path":{"type":"string"},` +
		`"item_type":{"type":"string","enum":["file","folder"]},` +
		`"recipient":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},` +
		`"required":["id"],"additionalProperties":false},"rights":` + rightsSchema + `,` +
		`"expires_at":{"type":"string"},"note":{"type":"string"},"label":{"type":"string"},` +
		`"has_password":{"type":"boolean"}},` +
		`"required":["id","type","direction","path","rights","has_password"],"additionalProperties":false}`
	shareIDSchema = `{"type":"string","pattern":"^[0-9]{1,18}$"}`
)

var shareFields = []capability.Field{
	{Name: "id", Description: "Share ID"},
	{Name: "type", Description: "Share type as a name: user, group, link, email, federated (including remote " +
		"groups), team, talk, or other for every other type"},
	{Name: "direction", Description: "outgoing for a share the identity owns, incoming for one made to it"},
	{Name: "path", Description: "Shared item relative to the fixed root folder of this connection; for an " +
		"incoming share its place in the Files tree of the identity"},
	{Name: "item_type", Description: "Either file or folder"},
	{Name: "recipient", Description: "Who the share reaches (identifier and display name, untrusted data); " +
		"absent for a link"},
	{Name: "rights", Description: "read, update, create, delete, and share as flags"},
	{Name: "expires_at", Description: "Expiry in RFC 3339 UTC when the share has one"},
	{Name: "note", Description: "Note to the recipient, untrusted data"},
	{Name: "label", Description: "Label of the share, untrusted data"},
	{Name: "has_password", Description: "True when the share is protected by a password; the password, the " +
		"link token, and the link URL are never reported"},
}

var sharesRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: sharesSensitivity,
}

var sharesList = capability.Descriptor{
	ID: Provider + ".shares.list", Version: 1, Title: "List Nextcloud shares",
	Description: "List the shares of an explicit Nextcloud connection whose item lies below its fixed root folder: " +
		"those the identity made, or with shared_with_me those made to it; every share type is reported " +
		"without link token, link URL, or password",
	Tags:     []string{"nextcloud", "shares", "sharing", "list", "ocs"},
	Risk:     sharesRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,` +
		`"subfiles":{"type":"boolean"},"shared_with_me":{"type":"boolean"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"shares":{"type":"array","items":` +
		shareSchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["shares","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Description: "File or folder relative to the fixed root folder of this connection; " +
			"without it all shares below the root are listed"},
		{Name: "subfiles", Description: "With path, list the shares of the items inside that folder instead of " +
			"its own; not with shared_with_me"},
		{Name: "shared_with_me", Description: "List the shares made to the identity instead of its own"},
	},
	Fields: []capability.Field{
		{Name: "shares", Description: "Shares below the root, untrusted data; shares elsewhere are omitted"},
		{Name: "count", Description: "Number of reported shares"},
		{Name: "truncated", Description: "True when more than 500 shares matched and the list was cut"},
	},
	Examples: []capability.Example{{
		Description: "List the shares made to the identity below the root",
		Arguments:   json.RawMessage(`{"shared_with_me":true}`),
	}},
}

var sharesGet = capability.Descriptor{
	ID: Provider + ".shares.get", Version: 1, Title: "Get a Nextcloud share",
	Description: "Read one share by its ID, if its item lies below the fixed root folder of an explicit " +
		"Nextcloud connection; any other share is reported as not found",
	Tags:     []string{"nextcloud", "shares", "sharing", "get", "ocs"},
	Risk:     sharesRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"share_id":` + shareIDSchema + `},` +
		`"required":["share_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(shareSchema),
	Arguments: []capability.Argument{
		{Name: "share_id", Description: "Numeric share ID as reported by shares.list", Required: true},
	},
	Fields: shareFields,
	Examples: []capability.Example{{
		Description: "Read one share a listing reported", Arguments: json.RawMessage(`{"share_id":"42"}`),
	}},
}

var shareeEntrySchema = `{"type":"object","properties":{"type":{"type":"string",` +
	`"enum":["user","group","link","email","federated","team","talk","other"]},"id":{"type":"string"},` +
	`"name":{"type":"string"}},"required":["type","id"],"additionalProperties":false}`

var shareesSearch = capability.Descriptor{
	ID: Provider + ".sharees.search", Version: 1, Title: "Search Nextcloud recipients",
	Description: "Search the possible share recipients of the whole Nextcloud instance by name; the directory " +
		"is instance-wide, needs an account target, and never asks the global lookup server",
	Tags:     []string{"nextcloud", "shares", "sharing", "sharees", "search", "directory", "ocs"},
	Provider: Provider,
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: shareeSensitivity,
	},
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"search":{"type":"string","minLength":1,"maxLength":128},` +
		`"item_type":{"type":"string","enum":["file","folder"]},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":50}},` +
		`"required":["search"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"sharees":{"type":"array","items":` +
		shareeEntrySchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["sharees","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "search", Description: "Part of a name, user ID, or address to look for", Required: true},
		{Name: "item_type", Description: "file or folder, which narrows the recipients to those the instance " +
			"allows for it; file when omitted"},
		{Name: "per_page", Description: "Candidates per kind, 1 to 50; 10 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "sharees", Description: "Candidates: share type name, identifier, and display name, personal " +
			"data and untrusted; at most 200"},
		{Name: "count", Description: "Number of reported candidates"},
		{Name: "truncated", Description: "True when more candidates matched than are reported"},
	},
	Examples: []capability.Example{{
		Description: "Look for a colleague", Arguments: json.RawMessage(`{"search":"alice"}`),
	}},
}

type sharesListArguments struct {
	Path         string `json:"path"`
	Subfiles     bool   `json:"subfiles"`
	SharedWithMe bool   `json:"shared_with_me"`
}

func invokeSharesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input sharesListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list shares", "the validated arguments could not be read")
	}
	if input.Subfiles && (input.Path == "" || input.SharedWithMe) {
		return nil, providerError("list shares", "subfiles needs a path and cannot be combined with shared_with_me")
	}
	if _, err := splitRelative(input.Path); err != nil {
		return nil, providerError("list shares", err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListShares(ctx, input.Path, input.Subfiles, input.SharedWithMe)
}

func invokeSharesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		ShareID string `json:"share_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get share", "the validated arguments could not be read")
	}
	if err := checkShareID(input.ShareID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetShare(ctx, input.ShareID)
}

func invokeShareesSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		Search   string `json:"search"`
		ItemType string `json:"item_type"`
		PerPage  *int   `json:"per_page"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("search sharees", "the validated arguments could not be read")
	}
	if input.ItemType == "" {
		input.ItemType = "file"
	}
	perPage := defaultPerPage
	if input.PerPage != nil {
		perPage = *input.PerPage
	}
	if input.Search == "" || len(input.Search) > maxSearchLength || perPage < 1 || perPage > maxPerPage ||
		input.ItemType != "file" && input.ItemType != "folder" {
		return nil, providerError("search sharees", "the search arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.SearchSharees(ctx, input.Search, input.ItemType, perPage)
}
