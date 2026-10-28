package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// userDataSensitivity marks the personal data (email address, name, role) the user tools read and change.
const userDataSensitivity = "n8n-users-personal"

// usersInstanceWide names the capability in a refusal by requireInstanceScope.
const usersInstanceWide = "user management"

// maxUserFieldLength bounds every string of a listed user, whatever n8n sends.
const maxUserFieldLength = 256

// userRoles is the fixed allow-list of instance roles a caller may assign. global:owner is
// deliberately not on it, and no free-form role name the API would also accept.
var userRoles = []string{"global:admin", "global:member", "global:chatUser"}

// userPermissionMessage is the one message of a 403 on a users endpoint. Only the instance owner may use
// these endpoints, and the API key may lack the user scope or the license; the body is never read.
const userPermissionMessage = "n8n refused this user management operation: only the instance owner may " +
	"use it, and the license or the API key's user scope may be missing; Qatlas cannot tell which"

var userReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: userDataSensitivity}

func userChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: userDataSensitivity}
}

var userRoleSchema = func() string {
	quoted := make([]string, len(userRoles))
	for i, r := range userRoles {
		quoted[i] = strconv.Quote(r)
	}
	return `{"type":"string","enum":[` + strings.Join(quoted, ",") + `]}`
}()

var userItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"email":{"type":"string"},` +
	`"first_name":{"type":"string"},"last_name":{"type":"string"},"role":{"type":"string"},` +
	`"is_pending":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","email","is_pending"],"additionalProperties":false}`

var userIDArgument = capability.Argument{Name: "user_id",
	Description: "n8n user identifier; an email address is not accepted", Required: true}

var userRoleArgument = capability.Argument{Name: "role",
	Description: "Instance role: " + strings.Join(userRoles, ", ") + "; global:owner is never assigned",
	Required:    true}

const userInstanceNote = "Only the instance owner may use user management, and only on a connection without " +
	"project or workflow targets (instance-wide)"

var userFields = []capability.Field{
	{Name: "id", Description: "User identifier"},
	{Name: "email", Description: "Email address, personal data, untrusted"},
	{Name: "first_name", Description: "First name, personal data, untrusted"},
	{Name: "last_name", Description: "Last name, personal data, untrusted"},
	{Name: "role", Description: "Instance role as n8n reports it"},
	{Name: "is_pending", Description: "True while the invited user has not finished setting up the account"},
	{Name: "created_at", Description: "Creation time, as n8n reports it"},
	{Name: "updated_at", Description: "Last update time, as n8n reports it"},
}

var usersList = capability.Descriptor{
	ID: Provider + ".users.list", Version: 1, Title: "List n8n users",
	Description: "List the users of the bound n8n instance with id, email, name, instance role, and pending " +
		"status, page by page with an opaque cursor. " + userInstanceNote,
	Tags: []string{"n8n", "users", "list", "automation"}, Risk: userReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"project_id":` + targetIDSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"users":{"type":"array","items":` + userItemSchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["users","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page"},
		{Name: "limit", Description: "Users per page, 1 to 250; 100 when omitted"},
		{Name: "project_id", Description: "Only users of this project; n8n answers not found for an unknown project"},
	},
	Fields: append([]capability.Field{{Name: "users", Description: "Users on this page"}}, append(userFields,
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains"},
		capability.Field{Name: "count", Description: "Number of users on this page"})...),
	Examples: []capability.Example{{Description: "List the first page of users",
		Arguments: json.RawMessage(`{}`)}},
}

var usersGet = capability.Descriptor{
	ID: Provider + ".users.get", Version: 1, Title: "Read an n8n user",
	Description: "Read one user of the bound n8n instance by ID, not by email. " + userInstanceNote,
	Tags:        []string{"n8n", "users", "get", "automation"}, Risk: userReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + targetIDSchema + `},` +
		`"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(userItemSchema),
	Arguments:    []capability.Argument{userIDArgument},
	Fields:       userFields,
	Examples: []capability.Example{{Description: "Read a user",
		Arguments: json.RawMessage(`{"user_id":"123e4567-e89b-12d3-a456-426614174000"}`)}},
}

var usersSetRole = capability.Descriptor{
	ID: Provider + ".users.set-role", Version: 1, Title: "Change an n8n user's instance role",
	Description: "Set the instance role of one existing user (global:admin, global:member, global:chatUser; " +
		"never global:owner). " + userInstanceNote,
	Tags: []string{"n8n", "users", "role", "automation"},
	Risk: userChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + targetIDSchema + `,` +
		`"role":` + userRoleSchema + `},"required":["user_id","role"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string"},` +
		`"role":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["user_id","role","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{userIDArgument, userRoleArgument},
	Fields: []capability.Field{{Name: "user_id", Description: "The user"},
		{Name: "role", Description: "The role that was sent"},
		{Name: "updated", Description: "True when n8n accepted the change"}},
	Examples: []capability.Example{{Description: "Make a user an admin",
		Arguments: json.RawMessage(`{"user_id":"123e4567-e89b-12d3-a456-426614174000","role":"global:admin"}`)}},
}

var usersDelete = capability.Descriptor{
	ID: Provider + ".users.delete", Version: 1, Title: "Delete an n8n user",
	Description: "Delete one user of the bound n8n instance. Exactly one consequence must be chosen: " +
		"transfer_project_id moves the user's workflows and credentials into that project, or " +
		"delete_owned_resources: true deletes them permanently together with the user. Neither, both, or " +
		"delete_owned_resources: false without transfer_project_id is refused locally. Where the connection " +
		"excludes projects or workflows, a transfer project outside what it reaches is refused, and so is " +
		"deleting the resources of a user whose personal project is excluded or owns an excluded workflow; " +
		"membership of an excluded project, or of a project that owns an excluded workflow, is read as " +
		"ownership, and anything that cannot be proven refuses. " + userInstanceNote,
	Tags: []string{"n8n", "users", "delete", "automation"},
	Risk: userChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + targetIDSchema + `,` +
		`"transfer_project_id":` + targetIDSchema + `,"delete_owned_resources":{"type":"boolean"}},` +
		`"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string"},` +
		`"deleted":{"type":"boolean"},"consequence":{"type":"string","enum":["transferred","deleted_with_user"]},` +
		`"transfer_project_id":{"type":"string"}},` +
		`"required":["user_id","deleted","consequence"],"additionalProperties":false}`),
	Arguments: []capability.Argument{userIDArgument,
		{Name: "transfer_project_id", Description: "Project ID that receives the user's workflows and credentials"},
		{Name: "delete_owned_resources",
			Description: "true to delete the user's workflows and credentials permanently; exclusive with " +
				"transfer_project_id"}},
	Fields: []capability.Field{{Name: "user_id", Description: "Identifier of the deleted user"},
		{Name: "deleted", Description: "True when n8n accepted the deletion"},
		{Name: "consequence", Description: "transferred, or deleted_with_user for the owned resources"},
		{Name: "transfer_project_id", Description: "The receiving project, when transferred"}},
	Examples: []capability.Example{{Description: "Delete a user and move their resources to a project",
		Arguments: json.RawMessage(`{"user_id":"123e4567-e89b-12d3-a456-426614174000",` +
			`"transfer_project_id":"VmwOO9HeTEj20kxM"}`)}},
}

// instanceUserJSON deliberately has no mfaEnabled: another user's MFA state is account-security data.
type instanceUserJSON struct {
	ID        string  `json:"id"`
	Email     *string `json:"email"`
	FirstName *string `json:"firstName"`
	LastName  *string `json:"lastName"`
	IsPending bool    `json:"isPending"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Role      string  `json:"role"`
}

// InstanceUser is the stable Qatlas view of one user. Email and names are untrusted personal data.
type InstanceUser struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Role      string `json:"role,omitempty"`
	IsPending bool   `json:"is_pending"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// InstanceUsersPage is one paginated listing of users.
type InstanceUsersPage struct {
	Users   []InstanceUser `json:"users"`
	Cursor  string         `json:"cursor,omitempty"`
	HasMore bool           `json:"has_more"`
	Count   int            `json:"count"`
}

// UserRoleChanged is what users.set-role reports.
type UserRoleChanged struct {
	UserID  string `json:"user_id"`
	Role    string `json:"role"`
	Updated bool   `json:"updated"`
}

// UserDeleted is what users.delete reports.
type UserDeleted struct {
	UserID            string `json:"user_id"`
	Deleted           bool   `json:"deleted"`
	Consequence       string `json:"consequence"`
	TransferProjectID string `json:"transfer_project_id,omitempty"`
}

func userText(value *string) string {
	if value == nil {
		return ""
	}
	if utf8.RuneCountInString(*value) > maxUserFieldLength {
		return string([]rune(*value)[:maxUserFieldLength])
	}
	return *value
}

func summarizeInstanceUser(u instanceUserJSON) InstanceUser {
	return InstanceUser{ID: bounded(u.ID), Email: userText(u.Email), FirstName: userText(u.FirstName),
		LastName: userText(u.LastName), Role: bounded(u.Role), IsPending: u.IsPending,
		CreatedAt: bounded(u.CreatedAt), UpdatedAt: bounded(u.UpdatedAt)}
}

// userError replaces the generic 403 message with the neutral owner, license, or scope message.
func userError(err error) error {
	if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassPermission {
		failure.Message = userPermissionMessage
	}
	return err
}

func validUserRole(role string) error {
	for _, allowed := range userRoles {
		if role == allowed {
			return nil
		}
	}
	return invalidRequest("role must be one of " + strings.Join(userRoles, ", "))
}

func validUserID(id string) error {
	if !validTargetID(id) {
		return invalidRequest("user_id must be a usable n8n identifier")
	}
	return nil
}

type userArguments struct {
	UserID              string `json:"user_id"`
	Role                string `json:"role"`
	TransferProjectID   string `json:"transfer_project_id"`
	ProjectID           string `json:"project_id"`
	DeleteOwnedResource *bool  `json:"delete_owned_resources"`
	Cursor              string `json:"cursor"`
	Limit               int    `json:"limit"`
}

// prepareUser reads the arguments and applies the instance-wide gate, before any secret or request.
func prepareUser(op string, resolved *config.Resolved, raw json.RawMessage) (userArguments, error) {
	var input userArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if err := requireInstanceScope(resolved, usersInstanceWide); err != nil {
		return input, err
	}
	return input, nil
}

func invokeUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := prepareUser("list users", resolved, raw)
	if err != nil {
		return nil, err
	}
	if input.ProjectID != "" && !validTargetID(input.ProjectID) {
		return nil, invalidRequest("project_id must be a usable n8n identifier")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	page, err := client.ListUsers(ctx, input.Cursor, input.ProjectID, limit)
	return page, userError(err)
}

// ListUsers reads one page of GET /users, with the role included and, when projectID is set, only the
// users of that project.
func (c *Client) ListUsers(ctx context.Context, cursor, projectID string, limit int) (*InstanceUsersPage, error) {
	const op = "list users"
	query := url.Values{"limit": {strconv.Itoa(limit)}, "includeRole": {"true"}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if projectID != "" {
		query.Set("projectId", projectID)
	}
	var page struct {
		Data       []instanceUserJSON `json:"data"`
		NextCursor *string            `json:"nextCursor"`
	}
	if err := c.get(ctx, op, "/users", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	next := ""
	if page.NextCursor != nil {
		if len(*page.NextCursor) > maxCursorLength {
			return nil, invalidResponse(op, "n8n reported an oversized page cursor")
		}
		next = *page.NextCursor
	}
	users := make([]InstanceUser, 0, len(page.Data))
	for _, u := range page.Data {
		users = append(users, summarizeInstanceUser(u))
	}
	return &InstanceUsersPage{Users: users, Cursor: next, HasMore: next != "", Count: len(users)}, nil
}

func invokeUsersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read user"
	input, err := prepareUser(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := validUserID(input.UserID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var user instanceUserJSON
	if err := client.get(ctx, op, "/users/"+url.PathEscape(input.UserID), url.Values{"includeRole": {"true"}},
		&user, maxResponseBytes); err != nil {
		return nil, userError(err)
	}
	return summarizeInstanceUser(user), nil
}

func invokeUsersSetRole(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "change user role"
	input, err := prepareUser(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := validUserID(input.UserID); err != nil {
		return nil, err
	}
	if err := validUserRole(input.Role); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.change(ctx, op, http.MethodPatch, "/users/"+url.PathEscape(input.UserID)+"/role", nil,
		map[string]string{"newRoleName": input.Role}, nil, maxResponseBytes); err != nil {
		return nil, userError(err)
	}
	return &UserRoleChanged{UserID: input.UserID, Role: input.Role, Updated: true}, nil
}

func invokeUsersDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete user"
	input, err := prepareUser(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := validUserID(input.UserID); err != nil {
		return nil, err
	}
	deleteResources := input.DeleteOwnedResource != nil && *input.DeleteOwnedResource
	transfer := input.TransferProjectID != ""
	if input.DeleteOwnedResource != nil && !*input.DeleteOwnedResource && !transfer {
		return nil, invalidRequest("delete_owned_resources: false chooses nothing; give transfer_project_id " +
			"or delete_owned_resources: true")
	}
	if transfer == deleteResources {
		return nil, invalidRequest("give exactly one of transfer_project_id (moves the user's workflows and " +
			"credentials into that project) or delete_owned_resources: true (deletes them with the user)")
	}
	if transfer && !validTargetID(input.TransferProjectID) {
		return nil, invalidRequest("transfer_project_id must be a usable n8n identifier")
	}
	if transfer {
		if err := selectProjectTarget(resolved, input.TransferProjectID); err != nil {
			return nil, err
		}
	}
	var query url.Values
	result := &UserDeleted{UserID: input.UserID, Deleted: true, Consequence: "deleted_with_user"}
	if transfer {
		query = url.Values{"transferId": {input.TransferProjectID}}
		result.Consequence, result.TransferProjectID = "transferred", input.TransferProjectID
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if deleteResources {
		if err := client.refuseUserOwningExcluded(ctx, op, input.UserID); err != nil {
			return nil, err
		}
	}
	if err := client.change(ctx, op, http.MethodDelete, "/users/"+url.PathEscape(input.UserID), query, nil, nil,
		maxResponseBytes); err != nil {
		return nil, userError(err)
	}
	return result, nil
}

// maxMemberLookupPages bounds the member pages read to place one user in one project.
const maxMemberLookupPages = 20

// userInProject reports whether the user is a member of the project. n8n deletes the resources of the
// personal project of a user, and the Public API names no personal project of a user, so membership is the
// proof. A project that cannot be read or paged to its end counts as one the user belongs to; only a
// failure that says nothing about the project (transport, rate limit) ends the call.
func (c *Client) userInProject(ctx context.Context, op, projectID, userID string) (bool, error) {
	cursor := ""
	for i := 0; i < maxMemberLookupPages; i++ {
		page, err := c.ListMembers(ctx, projectID, cursor, maxListLimit)
		if err != nil {
			if failure, ok := err.(*provider.Error); ok && (failure.Class == provider.ClassNotFound ||
				failure.Class == provider.ClassPermission || failure.Class == provider.ClassInvalidResponse) {
				return true, nil
			}
			return false, err
		}
		for _, member := range page.Members {
			if member.ID == userID {
				return true, nil
			}
		}
		if !page.HasMore {
			return false, nil
		}
		cursor = page.Cursor
	}
	return true, nil
}

// refuseUserOwningExcluded stops the deletion of a user together with their resources when that would delete
// something excluded: the user belongs to an excluded project, or to a project that owns an excluded
// workflow. This is narrower than n8n, which deletes only the personal project, because the type of a
// project is not part of this proof. Transferring the resources to a reachable project does not come here.
// The refusal names neither the user's project nor the excluded object.
func (c *Client) refuseUserOwningExcluded(ctx context.Context, op, userID string) error {
	if len(c.scope.excludedProjects) == 0 && len(c.scope.excludedWorkflows) == 0 {
		return nil
	}
	refusal := invalidRequest("the user owns objects outside the targets of this connection, so their " +
		"resources cannot be deleted")
	checked := map[string]bool{}
	member := func(projectID string) (bool, error) {
		if checked[projectID] {
			return false, nil
		}
		in, err := c.userInProject(ctx, op, projectID, userID)
		checked[projectID] = !in
		return in, err
	}
	for _, projectID := range c.scope.excludedProjects {
		if in, err := member(projectID); err != nil || in {
			return orRefusal(err, refusal)
		}
	}
	for _, workflowID := range c.scope.excludedWorkflows {
		workflow, err := c.fetchWorkflow(ctx, op, workflowID)
		if err != nil {
			if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassNotFound {
				continue
			}
			return err
		}
		owners, placed := workflow.ownerProjectIDs()
		if !placed {
			return refusal
		}
		for _, projectID := range owners {
			if in, err := member(projectID); err != nil || in {
				return orRefusal(err, refusal)
			}
		}
	}
	return nil
}

// orRefusal returns err when there is one and the refusal otherwise.
func orRefusal(err, refusal error) error {
	if err != nil {
		return err
	}
	return refusal
}
