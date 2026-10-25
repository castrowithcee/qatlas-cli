package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var userSentOnce = sentOnceFor("user", "matomo.users.get")

func userChangeRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: usersSensitive}
}

const userLoginSchema = `"login":{"type":"string","minLength":1,"maxLength":100}`

var usersDelete = capability.Descriptor{
	ID: Provider + ".users.delete", Version: 1, Title: "Delete a Matomo user",
	Description: "Delete one user and the access the user holds (UsersManager.deleteUser). It cannot be undone. It needs " +
		"admin or Super User rights and a connection whose targets name the instance. An admin may delete only a " +
		"pending user the admin invited; any other login is refused as not found. Matomo never deletes the only Super " +
		"User and rejects anonymous. Matomo emails the deleting user. A personal token needs no password confirmation " +
		"and the tool takes no password. The user is not read first; sent once and never repeated; offered only where " +
		"a connection's tools list names it",
	Tags: []string{"matomo", "users", "delete", "instance", "personal-data"}, Risk: userChangeRisk(capability.EffectDelete),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + userLoginSchema +
		`},"required":["login"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"login":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["login","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "login", Required: true,
		Description: "Login of the user to delete, from matomo.users.list, 1 to 100 characters; anonymous is refused"}},
	Fields: []capability.Field{
		{Name: "login", Description: "Login of the deleted user, untrusted"},
		{Name: "deleted", Description: "True once Matomo deleted the user"},
	},
	Examples: []capability.Example{{Description: "Delete the pending user invited-colleague",
		Arguments: json.RawMessage(`{"login":"invited-colleague"}`)}},
}

var usersLogout = capability.Descriptor{
	ID: Provider + ".users.logout", Version: 1, Title: "Log a Matomo user out",
	Description: "End all web sessions of one user (UsersManager.logoutUser); the user must log in again. Personal " +
		"tokens stay valid. It needs Super User rights and a connection whose targets name the instance, and rejects " +
		"anonymous. A personal token needs no password confirmation and the tool takes no password. Repeating it " +
		"ends the sessions again; sent once and never repeated by Qatlas",
	Tags: []string{"matomo", "users", "logout", "sessions", "instance", "personal-data"},
	Risk: userChangeRisk(capability.EffectExecute), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + userLoginSchema +
		`},"required":["login"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"login":{"type":"string"},"logged_out":{"type":"boolean"}},` +
		`"required":["login","logged_out"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "login", Required: true,
		Description: "Login of the user to log out, from matomo.users.list, 1 to 100 characters; anonymous is refused"}},
	Fields: []capability.Field{
		{Name: "login", Description: "Login of the user, untrusted"},
		{Name: "logged_out", Description: "True once Matomo ended the sessions"},
	},
	Examples: []capability.Example{{Description: "Log the user colleague out", Arguments: json.RawMessage(`{"login":"colleague"}`)}},
}

// UserDeleted is the answer of users.delete.
type UserDeleted struct {
	Login   string `json:"login"`
	Deleted bool   `json:"deleted"`
}

// UserLoggedOut is the answer of users.logout.
type UserLoggedOut struct {
	Login     string `json:"login"`
	LoggedOut bool   `json:"logged_out"`
}

// manageUser runs one fixed UsersManager method for a login. The checks come before any secret or request;
// passwordConfirmation is never sent.
func manageUser(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, tool, op, method string) (string, error) {
	var args struct {
		Login *string `json:"login"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return "", err
	}
	if args.Login == nil || !validLogin(*args.Login) {
		return "", invalidArgument("login must hold 1 to 100 characters without control characters")
	}
	// Matomo refuses anonymous itself; the tool refuses it before any request.
	if strings.EqualFold(strings.TrimSpace(*args.Login), "anonymous") {
		return "", invalidArgument("this login cannot be used with this tool")
	}
	if err := needsInstance(resolved); err != nil {
		return "", err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return "", err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, baseRequirement(tool), method, url.Values{"userLogin": {*args.Login}}, userSentOnce,
		&answer); err != nil {
		return "", err
	}
	if !isSuccessAnswer(answer) {
		return "", unclearChange(op, "Matomo returned no confirmation of the change", userSentOnce)
	}
	return *args.Login, nil
}

func invokeUsersDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	login, err := manageUser(ctx, resolved, secrets, red, raw, usersDelete.ID, "delete user", "UsersManager.deleteUser")
	if err != nil {
		return nil, err
	}
	return &UserDeleted{Login: login, Deleted: true}, nil
}

func invokeUsersLogout(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	login, err := manageUser(ctx, resolved, secrets, red, raw, usersLogout.ID, "log out user", "UsersManager.logoutUser")
	if err != nil {
		return nil, err
	}
	return &UserLoggedOut{Login: login, LoggedOut: true}, nil
}
