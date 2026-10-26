package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	ldapUsersSensitive = "matomo-ldap-users"
	maxLDAPText        = 500
	maxLDAPLogins      = 10000
)

func ldapRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: "LoginLdap"}
}

var ldapUsersCount = capability.Descriptor{
	ID: Provider + ".ldap-users.count", Version: 1, Title: "Count Matomo LDAP users",
	Description: "Count the users in the LDAP directory that the LoginLdap plugin is configured for, either those " +
		"matching an LDAP search filter (filter, LoginLdap.getCountOfUsersMatchingFilter) or those that are " +
		"members of one group (memberOf, LoginLdap.getCountOfUsersMemberOf); give exactly one. Matomo contacts " +
		"the external directory with its configured server and bind account; nothing is created or synchronized. " +
		"It needs Super User rights, which Matomo checks, and a connection whose targets name the instance; " +
		"offered only where a connection's tools list names it. The count says how many entries match, not who",
	Tags: []string{"matomo", "ldap-users", "count", "superuser", "instance"}, Risk: readRisk(ldapUsersSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"filter":{"type":"string","minLength":1,"maxLength":500},` +
		`"memberOf":{"type":"string","minLength":1,"maxLength":500}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","minimum":0}},` +
		`"required":["count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "filter", Description: "LDAP search filter such as (objectClass=person), 1 to 500 characters without " +
			"control characters; exclusive with memberOf"},
		{Name: "memberOf", Description: "Value of the group membership field, such as a group DN, 1 to 500 " +
			"characters without control characters; exclusive with filter"},
	},
	Fields:   []capability.Field{{Name: "count", Description: "Number of matching LDAP entries"}},
	Examples: []capability.Example{{Description: "Count persons", Arguments: json.RawMessage(`{"filter":"(objectClass=person)"}`)}},
}

var ldapUsersList = capability.Descriptor{
	ID: Provider + ".ldap-users.list", Version: 1, Title: "List Matomo LDAP users",
	Description: "List the Matomo logins already stored in the database as LDAP users " +
		"(LoginLdap.getExistingLdapUsersFromDb); the external directory is not contacted. It needs Super User " +
		"rights, which Matomo checks, and a connection whose targets name the instance; offered only where a " +
		"connection's tools list names it. Logins are personal data and untrusted, cut at 100 characters and " +
		"ordered; at most 10000 are read",
	Tags: []string{"matomo", "ldap-users", "list", "superuser", "personal-data", "instance"},
	Risk: readRisk(ldapUsersSensitive), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"logins":{"type":"array","items":{"type":"string"}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},"required":["logins","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: pagingArguments,
	Fields: append([]capability.Field{
		{Name: "logins", Description: "Matomo logins of LDAP users, untrusted personal data, ordered"}}, pagingFields...),
	Examples: []capability.Example{{Description: "List the LDAP users", Arguments: json.RawMessage(`{}`)}},
}

// LDAPUsersCount is the answer of ldap-users.count.
type LDAPUsersCount struct {
	Count int `json:"count"`
}

// LDAPUsersList is the answer of ldap-users.list.
type LDAPUsersList struct {
	Logins     []string `json:"logins"`
	HasMore    bool     `json:"has_more"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func invokeLDAPUsersCount(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "count LDAP users"
	var args struct {
		Filter   *string `json:"filter"`
		MemberOf *string `json:"memberOf"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if (args.Filter == nil) == (args.MemberOf == nil) {
		return nil, invalidArgument("give exactly one of filter and memberOf")
	}
	method, param, value := "LoginLdap.getCountOfUsersMatchingFilter", "filter", ""
	if args.Filter != nil {
		value = *args.Filter
	} else {
		method, param, value = "LoginLdap.getCountOfUsersMemberOf", "memberOf", *args.MemberOf
	}
	if !validText(value, maxLDAPText) {
		return nil, invalidArgument("filter or memberOf must hold 1 to 500 characters without control characters")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.instance {
		return nil, invalidArgument("this connection is not bound to the instance")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, ldapRequirement(ldapUsersCount.ID), method, url.Values{param: {value}}, &answer); err != nil {
		return nil, err
	}
	var count loose
	n := (*int)(nil)
	if scalar(answer, &count) == nil {
		n = count.number()
	}
	if n == nil || *n < 0 {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid count")
	}
	return &LDAPUsersCount{Count: *n}, nil
}

func invokeLDAPUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list LDAP users"
	var args struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(ldapUsersList.ID), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.instance {
		return nil, invalidArgument("this connection is not bound to the instance")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var rows []loose
	if err := client.call(ctx, op, ldapRequirement(ldapUsersList.ID), "LoginLdap.getExistingLdapUsersFromDb",
		url.Values{}, &rows); err != nil {
		return nil, err
	}
	logins := make([]string, 0, min(len(rows), maxLDAPLogins))
	for _, row := range rows {
		if len(logins) == maxLDAPLogins {
			break
		}
		if login := row.str(maxLoginLength); login != "" {
			logins = append(logins, login)
		}
	}
	slices.Sort(logins)
	batch, next := slice(paging, logins)
	if batch == nil {
		batch = []string{}
	}
	return &LDAPUsersList{Logins: batch, HasMore: next != "", NextCursor: next}, nil
}
