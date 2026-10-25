package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxRightSites        = 100
	maxRightCapabilities = 20
)

// setRoles are the roles users.access.set may grant; noaccess removes the access instead.
var setRoles = []string{"view", "write", "admin", "noaccess"}

var rightsSentOnce = sentOnceFor("access", "matomo.users.get-access")

const rightsSitesSchema = `"idSites":{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,` +
	`"items":{"type":"integer","minimum":1,"maximum":999999999}}`

const rightsCapabilitiesSchema = `"capabilities":{"type":"array","maxItems":20,"uniqueItems":true,` +
	`"items":{"type":"string","pattern":"` + accessPattern + `","maxLength":64}}`

var rightsLoginArgument = capability.Argument{Name: "login", Required: true,
	Description: "Login of an existing user, from matomo.users.list, 1 to 100 characters; anonymous and Super Users are refused"}

var rightsSitesArgument = capability.Argument{Name: "idSites", Required: true,
	Description: "1 to 100 website IDs, each inside the connection's targets; no all"}

var usersAccessSet = capability.Descriptor{
	ID: Provider + ".access.set", Version: 1, Title: "Set the access of a Matomo user",
	Description: "Give an existing user one role on websites, or remove the access with noaccess " +
		"(UsersManager.setUserAccess). It replaces every role and capability the user holds on those websites, " +
		"also when the new role is the old one; capabilities listed here are re-added. It needs an admin right on " +
		"every website, and the whole call is refused when one is outside the connection's targets. Matomo " +
		"refuses a Super User and a role it does not know. A personal token needs no password confirmation and the " +
		"tool takes no password. The anonymous user and Super User rights cannot be set; idempotent" + rightsSentOnce +
		"; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "users", "access", "roles", "update", "personal-data"},
	Risk: userChangeRisk(capability.EffectUpdate), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + userLoginSchema + `,` + rightsSitesSchema +
		`,"role":{"type":"string","enum":[` + quotedList(setRoles) + `]},` + rightsCapabilitiesSchema +
		`},"required":["login","idSites","role"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"login":{"type":"string"},` +
		`"idSites":{"type":"array","items":{"type":"integer"}},"role":{"type":"string"},` +
		`"capabilities":{"type":"array","items":{"type":"string"}},"updated":{"type":"boolean"}},` +
		`"required":["login","idSites","role","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{rightsLoginArgument, rightsSitesArgument,
		{Name: "role", Required: true, Description: "One of " + strings.Join(setRoles, ", ") + "; noaccess removes the access"},
		{Name: "capabilities", Description: "Capability IDs to hold besides the role, up to 20, from matomo.roles.list; " +
			"not with noaccess"}},
	Fields: []capability.Field{
		{Name: "login", Description: "Login of the user, untrusted"},
		{Name: "idSites", Description: "The websites that were changed"},
		{Name: "role", Description: "The role that was sent"},
		{Name: "capabilities", Description: "The capabilities that were sent"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Give colleague the view role on websites 1 and 2",
		Arguments: json.RawMessage(`{"login":"colleague","idSites":[1,2],"role":"view"}`)}},
}

var usersCapabilitiesAdd = capability.Descriptor{
	ID: Provider + ".capabilities.add", Version: 1, Title: "Add capabilities to a Matomo user",
	Description: "Give an existing user capabilities beyond the role on websites (UsersManager.addCapabilities). " +
		"The user needs a role on every website already; a capability the role includes is not stored again. It " +
		"needs an admin right on every website, and the whole call is refused when one is outside the connection's " +
		"targets. Matomo refuses a Super User, the anonymous user, and an unknown capability; idempotent" +
		rightsSentOnce + "; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "users", "capabilities", "update", "personal-data"},
	Risk: userChangeRisk(capability.EffectUpdate), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + userLoginSchema + `,` + rightsSitesSchema + `,` +
		strings.Replace(rightsCapabilitiesSchema, `"maxItems":20`, `"minItems":1,"maxItems":20`, 1) +
		`},"required":["login","idSites","capabilities"],"additionalProperties":false}`),
	OutputSchema: rightsOutputSchema,
	Arguments: []capability.Argument{rightsLoginArgument, rightsSitesArgument,
		{Name: "capabilities", Required: true, Description: "1 to 20 capability IDs from matomo.roles.list"}},
	Fields: rightsFields,
	Examples: []capability.Example{{Description: "Let colleague write tag manager containers on website 1",
		Arguments: json.RawMessage(`{"login":"colleague","idSites":[1],"capabilities":["tagmanager_write"]}`)}},
}

var usersCapabilitiesRemove = capability.Descriptor{
	ID: Provider + ".capabilities.remove", Version: 1, Title: "Remove capabilities from a Matomo user",
	Description: "Remove capabilities that were given to an existing user on websites " +
		"(UsersManager.removeCapabilities). A capability the role includes stays effective. It needs an admin " +
		"right on every website, and the whole call is refused when one is outside the connection's targets. " +
		"Matomo refuses an unknown capability; idempotent" + rightsSentOnce + "; offered only where a connection's " +
		"tools list names it",
	Tags: []string{"matomo", "users", "capabilities", "update", "personal-data"},
	Risk: userChangeRisk(capability.EffectUpdate), Provider: Provider, RequiresToolAllowList: true,
	InputSchema:  usersCapabilitiesAdd.InputSchema,
	OutputSchema: rightsOutputSchema,
	Arguments:    usersCapabilitiesAdd.Arguments,
	Fields:       rightsFields,
	Examples: []capability.Example{{Description: "Take tag manager write from colleague on website 1",
		Arguments: json.RawMessage(`{"login":"colleague","idSites":[1],"capabilities":["tagmanager_write"]}`)}},
}

var rightsOutputSchema = json.RawMessage(`{"type":"object","properties":{"login":{"type":"string"},` +
	`"idSites":{"type":"array","items":{"type":"integer"}},"capabilities":{"type":"array","items":{"type":"string"}},` +
	`"updated":{"type":"boolean"}},"required":["login","idSites","capabilities","updated"],"additionalProperties":false}`)

var rightsFields = []capability.Field{
	{Name: "login", Description: "Login of the user, untrusted"},
	{Name: "idSites", Description: "The websites that were changed"},
	{Name: "capabilities", Description: "The capabilities that were sent"},
	{Name: "updated", Description: "True once Matomo accepted the change"},
}

// UserAccessSet is the answer of users.access.set.
type UserAccessSet struct {
	Login        string   `json:"login"`
	IDSites      []int    `json:"idSites"`
	Role         string   `json:"role"`
	Capabilities []string `json:"capabilities,omitempty"`
	Updated      bool     `json:"updated"`
}

// UserCapabilitiesChanged is the answer of users.capabilities.add and users.capabilities.remove.
type UserCapabilitiesChanged struct {
	Login        string   `json:"login"`
	IDSites      []int    `json:"idSites"`
	Capabilities []string `json:"capabilities"`
	Updated      bool     `json:"updated"`
}

// rightsTarget is the validated login, websites, and capabilities of a rights change.
type rightsTarget struct {
	login        string
	sites        []int
	capabilities []string
}

// roleIDs can never travel as a capability: setUserAccess reads every entry of its access list as a role or
// capability, so a role among them could grant more than the role argument says.
var roleIDs = []string{"view", "write", "admin", "noaccess", "superuser"}

// parseRights checks everything before any secret is resolved or request is sent. Errors name no login, ID,
// or target.
func parseRights(resolved *config.Resolved, login *string, sites []int, capabilities []string, needCapabilities bool) (rightsTarget, error) {
	if login == nil || !validLogin(*login) {
		return rightsTarget{}, invalidArgument("login must hold 1 to 100 characters without control characters")
	}
	if strings.EqualFold(strings.TrimSpace(*login), "anonymous") {
		return rightsTarget{}, invalidArgument("this login cannot be used with this tool")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return rightsTarget{}, err
	}
	if !bound.reachesSites() {
		return rightsTarget{}, invalidArgument("this connection is bound to no website")
	}
	if len(sites) < 1 || len(sites) > maxRightSites {
		return rightsTarget{}, invalidArgument("idSites must hold 1 to 100 website IDs")
	}
	seen := map[int]bool{}
	for _, id := range sites {
		if id < 1 || len(strconv.Itoa(id)) > maxIDDigits {
			return rightsTarget{}, invalidArgument("idSites must be positive integers")
		}
		if !bound.allows(id) {
			return rightsTarget{}, invalidArgument("an ID in idSites is outside the targets of this connection")
		}
		if seen[id] {
			return rightsTarget{}, invalidArgument("idSites must not repeat an ID")
		}
		seen[id] = true
	}
	if len(capabilities) > maxRightCapabilities || needCapabilities && len(capabilities) == 0 {
		return rightsTarget{}, invalidArgument("capabilities holds too many or too few capability IDs")
	}
	for i, id := range capabilities {
		if !validAccessID(id) || slices.ContainsFunc(roleIDs, func(role string) bool { return strings.EqualFold(role, id) }) {
			return rightsTarget{}, invalidArgument("capabilities must be capability IDs of letters, digits, and underscores")
		}
		if slices.Index(capabilities, id) != i {
			return rightsTarget{}, invalidArgument("capabilities must not repeat an ID")
		}
	}
	return rightsTarget{login: *login, sites: sites, capabilities: capabilities}, nil
}

func (r rightsTarget) params() url.Values {
	ids := make([]string, len(r.sites))
	for i, id := range r.sites {
		ids[i] = strconv.Itoa(id)
	}
	return url.Values{"userLogin": {r.login}, "idSites": {strings.Join(ids, ",")}}
}

func sendRights(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, tool, method string, params url.Values) error {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, siteRequirement(tool), method, params, rightsSentOnce, &answer); err != nil {
		return err
	}
	if !isSuccessAnswer(answer) {
		return unclearChange(op, "Matomo returned no confirmation of the change", rightsSentOnce)
	}
	return nil
}

func invokeUsersAccessSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		Login        *string  `json:"login"`
		IDSites      []int    `json:"idSites"`
		Role         *string  `json:"role"`
		Capabilities []string `json:"capabilities"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.Role == nil || !slices.Contains(setRoles, *args.Role) {
		return nil, invalidArgument("role must be one of " + strings.Join(setRoles, ", "))
	}
	if *args.Role == "noaccess" && len(args.Capabilities) > 0 {
		return nil, invalidArgument("capabilities cannot be combined with noaccess")
	}
	target, err := parseRights(resolved, args.Login, args.IDSites, args.Capabilities, false)
	if err != nil {
		return nil, err
	}
	params := target.params()
	if len(target.capabilities) == 0 {
		params.Set("access", *args.Role)
	} else {
		params["access[]"] = append([]string{*args.Role}, target.capabilities...)
	}
	if err := sendRights(ctx, resolved, secrets, red, "set user access", usersAccessSet.ID, "UsersManager.setUserAccess",
		params); err != nil {
		return nil, err
	}
	return &UserAccessSet{Login: target.login, IDSites: target.sites, Role: *args.Role,
		Capabilities: target.capabilities, Updated: true}, nil
}

func invokeUsersCapabilities(descriptor capability.Descriptor, method, op string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var args struct {
			Login        *string  `json:"login"`
			IDSites      []int    `json:"idSites"`
			Capabilities []string `json:"capabilities"`
		}
		if err := decodeArguments(raw, &args); err != nil {
			return nil, err
		}
		target, err := parseRights(resolved, args.Login, args.IDSites, args.Capabilities, true)
		if err != nil {
			return nil, err
		}
		params := target.params()
		params["capabilities[]"] = target.capabilities
		if err := sendRights(ctx, resolved, secrets, red, op, descriptor.ID, method, params); err != nil {
			return nil, err
		}
		return &UserCapabilitiesChanged{Login: target.login, IDSites: target.sites,
			Capabilities: target.capabilities, Updated: true}, nil
	}
}
