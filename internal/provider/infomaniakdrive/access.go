package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// accessSensitivity classifies the access of a file as its own class: who may reach it and with which right
// is personal data about people, not metadata about the file, and it is not a link URL either.
const accessSensitivity = "infomaniak-kdrive-access"

// Rights a grant or an update may give, as the Infomaniak API defines them: read can only read, write can
// write and read, manage can also share. The API's "none" is deliberately absent: removing access is what
// access.revoke is for, so no call ends up as a silent revocation.
const (
	accessRead   = "read"
	accessWrite  = "write"
	accessManage = "manage"
)

// Bounds of the arguments, of the membership check, and of an answer.
const (
	maxGrantUsers = 20
	maxGrantTeams = 10
	// membersPerPage and maxMemberPages bound the membership check: at most this many requests and users are
	// read, and a target not found by then is refused as unproven rather than guessed at.
	membersPerPage = 100
	maxMemberPages = 10
	// maxAccessEntries and maxAccessText bound one list and one string of an answered access.
	maxAccessEntries = 200
	maxAccessText    = 256
	accessKindLength = 32
)

// Result states of a grant: partial means Infomaniak applied some of the targets and refused others.
const statusPartial = "partial"

const (
	accessRightSchema = `{"type":"string","enum":["read","write","manage"]}`
	userIDsSchema     = `{"type":"array","items":` + objectIDSchema + `,"minItems":1,"maxItems":20,"uniqueItems":true}`
	teamIDsSchema     = `{"type":"array","items":` + objectIDSchema + `,"minItems":1,"maxItems":10,"uniqueItems":true}`
)

var accessEntrySchema = `{"type":"object","properties":{"id":{"type":"integer"},"access":{"type":"string"},` +
	`"name":{"type":"string"},"right":{"type":"string"},"status":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

var accessListSchema = `{"type":"object","properties":{` +
	`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},` +
	`"users":{"type":"array","items":` + accessEntrySchema + `},` +
	`"teams":{"type":"array","items":` + accessEntrySchema + `},` +
	`"invitations":{"type":"array","items":` + accessEntrySchema + `},` +
	`"truncated":{"type":"boolean"}},` +
	`"required":["drive_id","file_id","users","teams","invitations","truncated"],"additionalProperties":false}`

var accessChangeSchema = `{"type":"object","properties":{` +
	`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},` +
	`"status":{"type":"string","enum":["done","partial","pending"]},` +
	`"user_id":{"type":"integer"},"team_id":{"type":"integer"},"right":` + accessRightSchema + `,` +
	`"granted":{"type":"integer"},"failed":{"type":"integer"},` +
	`"results":{"type":"array","items":{"type":"object","properties":{` +
	`"kind":{"type":"string","enum":["user","team","email"]},"target":{"type":"string"},"granted":{"type":"boolean"}},` +
	`"required":["kind","target","granted"],"additionalProperties":false}}},` +
	`"required":["drive_id","file_id","status"],"additionalProperties":false}`

var accessListFields = []capability.Field{
	{Name: "drive_id", Description: "Drive the file belongs to"},
	{Name: "file_id", Description: "File or folder whose access was read"},
	{Name: "users", Description: "Users with access: id, access kind, display name, right, and status; personal " +
		"data and untrusted, names are length-bounded and at most 200 are returned"},
	{Name: "teams", Description: "Teams with access, in the same form as users"},
	{Name: "invitations", Description: "Invited external people with access, in the same form as users; the name " +
		"of an invitation can be an e-mail address"},
	{Name: "truncated", Description: "True when a list held more than 200 entries and was cut, so it is not the " +
		"whole access"},
}

var accessChangeFields = []capability.Field{
	{Name: "drive_id", Description: "Drive the change was made in"},
	{Name: "file_id", Description: "File or folder whose access changed"},
	{Name: "status", Description: "done when Infomaniak applied the change; partial when a grant reached some " +
		"targets and not others, see results; pending when Infomaniak accepted it and has not finished it, so " +
		"read the access before relying on it"},
	{Name: "user_id", Description: "User whose access changed; only for an update or a revoke of a user"},
	{Name: "team_id", Description: "Team whose access changed; only for an update or a revoke of a team"},
	{Name: "right", Description: "Right that was set; absent for a revoke"},
	{Name: "granted", Description: "Number of targets a grant reached; absent for other changes"},
	{Name: "failed", Description: "Number of targets a grant did not reach; absent for other changes"},
	{Name: "results", Description: "Per target of a grant: kind, target as Infomaniak reports it (a user or team " +
		"ID, or the e-mail address, which is personal data), and whether it was reached; untrusted data"},
}

func accessChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: accessSensitivity}
}

var accessFileArgument = capability.Argument{Name: "file_id", Required: true,
	Description: "File or folder of the same drive; never the drive's root"}

var accessTargetArguments = []capability.Argument{
	{Name: "user_id", Description: "User of the same drive; give exactly one of user_id and team_id"},
	{Name: "team_id", Description: "Team with users in the same drive; give exactly one of user_id and team_id"},
}

const accessTargetProperties = `"user_id":` + objectIDSchema + `,"team_id":` + objectIDSchema

var accessGet = capability.Descriptor{
	ID:      Provider + ".access.get",
	Version: 1,
	Title:   "Get the Infomaniak kDrive access of a file or folder",
	Description: "Read who has access to exactly one file or folder of a drive this connection may reach: users, " +
		"teams, and invited external people with their right and status; this is personal data, so a connection " +
		"must list the tool by name",
	Tags: []string{"infomaniak", "kdrive", "access", "get"},
	Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: accessSensitivity},
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(accessListSchema),
	Arguments:    []capability.Argument{driveIDArgument, accessFileArgument},
	Fields:       accessListFields,
	Examples: []capability.Example{{Description: "Read the access of one folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":42}`)}},
}

var accessGrant = capability.Descriptor{
	ID:      Provider + ".access.grant",
	Version: 2,
	Title:   "Grant Infomaniak kDrive access to users or teams",
	Description: "Give exactly one confirmed access change to up to 20 users and 10 teams at once for a file or " +
		"folder of a drive this connection may reach; users and teams must belong to the same drive, and no " +
		"e-mail address is invited",
	Tags:                  []string{"infomaniak", "kdrive", "access", "grant"},
	Risk:                  accessChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,"right":` + accessRightSchema + `,"user_ids":` +
		userIDsSchema + `,"team_ids":` + teamIDsSchema +
		`},"required":["drive_id","file_id","right"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(accessChangeSchema),
	Arguments: []capability.Argument{mutationDriveArgument, accessFileArgument,
		{Name: "right", Required: true, Description: "read: only read; write: write and read; manage: also share"},
		{Name: "user_ids", Description: "Up to 20 users of the same drive"},
		{Name: "team_ids", Description: "Up to 10 teams with users in the same drive; at least one of user_ids and " +
			"team_ids is required"},
	},
	Fields: accessChangeFields,
	Examples: []capability.Example{{Description: "Give one user of the drive read access to a folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":42,"right":"read","user_ids":[7]}`)}},
}

var accessUpdate = capability.Descriptor{
	ID:      Provider + ".access.update",
	Version: 1,
	Title:   "Change an Infomaniak kDrive access right",
	Description: "Change exactly one confirmed access right of one user or one team of a drive this connection may " +
		"reach on a file or folder; the user or team must belong to the same drive",
	Tags:                  []string{"infomaniak", "kdrive", "access", "update"},
	Risk:                  accessChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,` + accessTargetProperties + `,"right":` + accessRightSchema +
		`},"required":["drive_id","file_id","right"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(accessChangeSchema),
	Arguments: append(append([]capability.Argument{mutationDriveArgument, accessFileArgument}, accessTargetArguments...),
		capability.Argument{Name: "right", Required: true,
			Description: "read: only read; write: write and read; manage: also share; removing access is access.revoke"}),
	Fields: accessChangeFields,
	Examples: []capability.Example{{Description: "Let one user write to a folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":42,"user_id":7,"right":"write"}`)}},
}

var accessRevoke = capability.Descriptor{
	ID:      Provider + ".access.revoke",
	Version: 1,
	Title:   "Revoke Infomaniak kDrive access",
	Description: "Remove exactly one confirmed access of one user or one team of a drive this connection may reach " +
		"from a file or folder; the user or team must belong to the same drive, and the file itself stays",
	Tags:                  []string{"infomaniak", "kdrive", "access", "revoke"},
	Risk:                  accessChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,` + accessTargetProperties + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(accessChangeSchema),
	Arguments: append([]capability.Argument{mutationDriveArgument, accessFileArgument},
		accessTargetArguments...),
	Fields: accessChangeFields,
	Examples: []capability.Example{{Description: "Remove the access of one team from a folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":42,"team_id":3}`)}},
}

// AccessEntry is one user, team, or invitation with access to a file, as Infomaniak reports it. Every string
// is untrusted data and bounded.
type AccessEntry struct {
	ID     int64  `json:"id"`
	Access string `json:"access,omitempty"`
	Name   string `json:"name"`
	Right  string `json:"right,omitempty"`
	Status string `json:"status,omitempty"`
}

// AccessList is the stable answer of an access read.
type AccessList struct {
	DriveID     int64         `json:"drive_id"`
	FileID      int64         `json:"file_id"`
	Users       []AccessEntry `json:"users"`
	Teams       []AccessEntry `json:"teams"`
	Invitations []AccessEntry `json:"invitations"`
	Truncated   bool          `json:"truncated"`
}

// AccessResult is one target of a grant and whether Infomaniak reached it.
type AccessResult struct {
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Granted bool   `json:"granted"`
}

// AccessChange is the stable answer of a grant, an update, or a revoke.
type AccessChange struct {
	DriveID int64          `json:"drive_id"`
	FileID  int64          `json:"file_id"`
	Status  string         `json:"status"`
	UserID  int64          `json:"user_id,omitempty"`
	TeamID  int64          `json:"team_id,omitempty"`
	Right   string         `json:"right,omitempty"`
	Granted *int           `json:"granted,omitempty"`
	Failed  *int           `json:"failed,omitempty"`
	Results []AccessResult `json:"results,omitempty"`
}

const accessFileReason = "file_id must be a positive integer other than the drive's root"

func accessPath(driveID, fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/access", driveID, fileID)
}

func validAccessRight(right string) bool {
	return right == accessRead || right == accessWrite || right == accessManage
}

// boundedText cuts a provider string to at most limit bytes on a rune boundary and drops control characters.
func boundedText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, value)
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// validIDList accepts identifiers that are valid, unique, and at most limit many.
func validIDList(ids []int64, limit int) bool {
	if len(ids) > limit {
		return false
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if !validObjectID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// memberJSON is the subset of one Infomaniak drive user this provider reads for the membership check.
type memberJSON struct {
	ID        int64   `json:"id"`
	DriveID   int64   `json:"drive_id"`
	Status    string  `json:"status"`
	DeletedAt int64   `json:"deleted_at"`
	Teams     []int64 `json:"teams"`
}

// verifyAccessTargets proves that every user and every team belongs to the drive, before a change names
// them. It reads the users of the drive page by page, with their team identifiers, up to a fixed bound, and
// it stops as soon as everything is found. A target not found on the last page is foreign; a target not
// found when the bound or an unreadable page count ends the search is unproven. Both are refused, and no
// refusal names the value. Infomaniak lists no teams of a drive on their own, so a team counts only through
// an active user of the drive who belongs to it.
func (c *Client) verifyAccessTargets(ctx context.Context, op string, driveID int64, userIDs, teamIDs []int64) error {
	if len(userIDs) == 0 && len(teamIDs) == 0 {
		return nil
	}
	missingUsers := map[int64]bool{}
	for _, id := range userIDs {
		missingUsers[id] = true
	}
	missingTeams := map[int64]bool{}
	for _, id := range teamIDs {
		missingTeams[id] = true
	}
	complete := false
	for page := 1; page <= maxMemberPages; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(membersPerPage)},
			"with": {"teams"}, "total": {"true"}}
		var members []memberJSON
		var meta paginationJSON
		if err := c.doInto(ctx, op, fmt.Sprintf("/2/drive/%d/users", driveID), query, &members, &meta); err != nil {
			return err
		}
		for _, member := range members {
			if member.Status != "active" || member.DeletedAt != 0 || member.DriveID != driveID {
				continue
			}
			delete(missingUsers, member.ID)
			for _, team := range member.Teams {
				delete(missingTeams, team)
			}
		}
		if len(missingUsers) == 0 && len(missingTeams) == 0 {
			return nil
		}
		if meta.Pages > 0 && page >= meta.Pages {
			complete = true
			break
		}
		if meta.Pages == 0 || len(members) == 0 {
			break
		}
	}
	switch {
	case !complete:
		return invalidRequest("the users and teams of this drive could not be read completely, so a user_id or " +
			"team_id could not be proven to belong to it")
	case len(missingUsers) > 0:
		return invalidRequest("a user_id is not a user of this drive")
	default:
		return invalidRequest("a team_id is not a team with users in this drive")
	}
}

func invokeAccessGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get access"
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(accessFileReason)
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.GetAccess(ctx, input.DriveID, input.FileID)
}

type accessEntryJSON struct {
	ID     int64   `json:"id"`
	Access string  `json:"access"`
	Name   string  `json:"name"`
	Right  *string `json:"right"`
	Status string  `json:"status"`
}

func accessEntriesOf(items []accessEntryJSON, truncated *bool) []AccessEntry {
	if len(items) > maxAccessEntries {
		items = items[:maxAccessEntries]
		*truncated = true
	}
	entries := make([]AccessEntry, 0, len(items))
	for _, item := range items {
		entry := AccessEntry{ID: item.ID, Access: boundedText(item.Access, accessKindLength),
			Name: boundedText(item.Name, maxAccessText), Status: boundedText(item.Status, accessKindLength)}
		if item.Right != nil {
			entry.Right = boundedText(*item.Right, accessKindLength)
		}
		entries = append(entries, entry)
	}
	return entries
}

// GetAccess reads the access of exactly one file or folder, bounded in entries and in text.
func (c *Client) GetAccess(ctx context.Context, driveID, fileID int64) (*AccessList, error) {
	const op = "get access"
	var raw struct {
		Users       []accessEntryJSON `json:"users"`
		Teams       []accessEntryJSON `json:"teams"`
		Invitations []accessEntryJSON `json:"invitations"`
	}
	if err := c.do(ctx, op, accessPath(driveID, fileID), nil, &raw); err != nil {
		return nil, err
	}
	list := &AccessList{DriveID: driveID, FileID: fileID}
	list.Users = accessEntriesOf(raw.Users, &list.Truncated)
	list.Teams = accessEntriesOf(raw.Teams, &list.Truncated)
	list.Invitations = accessEntriesOf(raw.Invitations, &list.Truncated)
	return list, nil
}

type accessGrantArguments struct {
	DriveID int64   `json:"drive_id"`
	FileID  int64   `json:"file_id"`
	Right   string  `json:"right"`
	UserIDs []int64 `json:"user_ids"`
	TeamIDs []int64 `json:"team_ids"`
}

func invokeAccessGrant(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "grant access"
	var input accessGrantArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(accessFileReason)
	}
	if !validAccessRight(input.Right) {
		return nil, invalidRequest("right must be read, write, or manage")
	}
	if !validIDList(input.UserIDs, maxGrantUsers) {
		return nil, invalidRequest("user_ids must be at most 20 different positive integers")
	}
	if !validIDList(input.TeamIDs, maxGrantTeams) {
		return nil, invalidRequest("team_ids must be at most 10 different positive integers")
	}
	if len(input.UserIDs)+len(input.TeamIDs) == 0 {
		return nil, invalidRequest("a grant needs at least one of user_ids and team_ids")
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyAccessTargets(ctx, op, input.DriveID, input.UserIDs, input.TeamIDs); err != nil {
		return nil, err
	}
	return client.GrantAccess(ctx, input.DriveID, input.FileID, input)
}

// feedbackJSON is the per-target answer of a grant. Its message is never read: it is provider text.
type feedbackJSON struct {
	ID     json.RawMessage `json:"id"`
	Result bool            `json:"result"`
}

func targetText(id json.RawMessage) string {
	var text string
	if json.Unmarshal(id, &text) == nil {
		return boundedText(text, maxAccessText)
	}
	var number int64
	if json.Unmarshal(id, &number) == nil {
		return strconv.FormatInt(number, 10)
	}
	return ""
}

// GrantAccess sends exactly one multi-access request, once. The body holds only the validated targets and
// right.
func (c *Client) GrantAccess(ctx context.Context, driveID, fileID int64, input accessGrantArguments) (*AccessChange, error) {
	const op = "grant access"
	body := map[string]any{"right": input.Right}
	if len(input.UserIDs) > 0 {
		body["user_ids"] = input.UserIDs
	}
	if len(input.TeamIDs) > 0 {
		body["team_ids"] = input.TeamIDs
	}
	env, status, err := c.accessRequest(ctx, op, http.MethodPost, accessPath(driveID, fileID),
		nil, body)
	if err != nil {
		return nil, err
	}
	result := &AccessChange{DriveID: driveID, FileID: fileID, Status: status, Right: input.Right}
	if status == statusPending {
		return result, nil
	}
	var feedback struct {
		Users []feedbackJSON `json:"users"`
		Teams []feedbackJSON `json:"teams"`
	}
	if json.Unmarshal(env.Data, &feedback) != nil {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+uncertain)
	}
	granted, failed := 0, 0
	for _, group := range []struct {
		kind  string
		items []feedbackJSON
		limit int
	}{{"user", feedback.Users, len(input.UserIDs)}, {"team", feedback.Teams, len(input.TeamIDs)}} {
		for i, item := range group.items {
			if i >= group.limit {
				break
			}
			result.Results = append(result.Results, AccessResult{Kind: group.kind, Target: targetText(item.ID),
				Granted: item.Result})
			if item.Result {
				granted++
			} else {
				failed++
			}
		}
	}
	if granted+failed == 0 {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+uncertain)
	}
	result.Granted, result.Failed = &granted, &failed
	if failed > 0 {
		result.Status = statusPartial
	}
	return result, nil
}

type accessChangeArguments struct {
	DriveID int64  `json:"drive_id"`
	FileID  int64  `json:"file_id"`
	UserID  int64  `json:"user_id"`
	TeamID  int64  `json:"team_id"`
	Right   string `json:"right"`
}

func invokeAccessUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeAccessOne(ctx, resolved, secrets, red, raw, "update access", true)
}

func invokeAccessRevoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeAccessOne(ctx, resolved, secrets, red, raw, "revoke access", false)
}

// invokeAccessOne runs the local checks of an update or a revoke in order: identifiers and right, the drive
// checks, the membership of the one user or team, and only then the one request.
func invokeAccessOne(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string, update bool) (any, error) {
	var input accessChangeArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(accessFileReason)
	}
	if (input.UserID == 0) == (input.TeamID == 0) {
		return nil, invalidRequest("give exactly one of user_id and team_id")
	}
	if input.UserID != 0 && !validObjectID(input.UserID) || input.TeamID != 0 && !validObjectID(input.TeamID) {
		return nil, invalidRequest("user_id and team_id must be positive integers")
	}
	switch {
	case update && !validAccessRight(input.Right):
		return nil, invalidRequest("right must be read, write, or manage")
	case !update && input.Right != "":
		return nil, invalidRequest("a revoke takes no right")
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	var users, teams []int64
	if input.UserID != 0 {
		users = []int64{input.UserID}
	} else {
		teams = []int64{input.TeamID}
	}
	if err := client.verifyAccessTargets(ctx, op, input.DriveID, users, teams); err != nil {
		return nil, err
	}
	return client.ChangeAccess(ctx, op, input, update)
}

// ChangeAccess updates or removes the access of exactly one user or team, once.
func (c *Client) ChangeAccess(ctx context.Context, op string, input accessChangeArguments, update bool) (*AccessChange, error) {
	path := accessPath(input.DriveID, input.FileID)
	if input.UserID != 0 {
		path += "/users/" + strconv.FormatInt(input.UserID, 10)
	} else {
		path += "/teams/" + strconv.FormatInt(input.TeamID, 10)
	}
	method, body := http.MethodDelete, any(nil)
	if update {
		method, body = http.MethodPut, map[string]any{"right": input.Right}
	}
	env, status, err := c.accessRequest(ctx, op, method, path, nil, body)
	if err != nil {
		return nil, err
	}
	if status == statusDone {
		var applied bool
		if json.Unmarshal(env.Data, &applied) != nil || !applied {
			return nil, invalidResponse(op, "Infomaniak returned an invalid response"+uncertain)
		}
	}
	return &AccessChange{DriveID: input.DriveID, FileID: input.FileID, Status: status, UserID: input.UserID,
		TeamID: input.TeamID, Right: input.Right}, nil
}

// accessRequest sends exactly one change and maps its envelope to done or pending; anything else is an
// unclear outcome. The method is fixed by the caller, never by an agent.
func (c *Client) accessRequest(ctx context.Context, op, method, path string, query url.Values,
	body any) (*envelope, string, error) {
	env, _, err := c.request(ctx, op, method, path, query, body, true)
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
