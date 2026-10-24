package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The organization people tools read and change who belongs to an organization, who is listed publicly, and who
// is blocked. The organization is bound exactly as in orgs.go. The user name is the person's identifier: it is
// one validated path segment and is not checked against the targets, because the answer concerns the
// organization. The people are personal data.

const (
	orgPeopleSensitivity = "-personal-data"
	maxBlockNote         = 255
)

var orgUserArgument = capability.Argument{Name: "username", Required: true,
	Description: "User name of the person"}

// blockedForgejoMembers are the members of one Forgejo block entry; Forgejo lists no user object.
var blockedForgejoMembers = []member{
	{name: "block_id", kind: kindInteger, note: "ID of the block entry"},
	{name: "created_at", kind: kindText, note: "Time of the block"},
}

// orgPeopleRows are the members of one row of a people list: a user object, or on Forgejo's block list a block
// entry.
func orgPeopleRows(p *product, blocks bool) []member {
	if blocks && p == forgejo {
		return blockedForgejoMembers
	}
	return personMembers
}

func orgUserOf(value string) error {
	if value == "" || !validName(value) || len(value) > 40 {
		return invalidArgument("username must be a user name")
	}
	return nil
}

const orgUserSchema = issueSchemaLogin

func orgUserProperties(extra string) string {
	out := orgProperties(`"username":` + orgUserSchema)
	if extra != "" {
		out += "," + extra
	}
	return out
}

func orgUserPath(org, user, base string) string {
	return orgPath(org) + "/" + base + "/" + escape(user)
}

// ---- lists --------------------------------------------------------------------------------------------------

type orgPeopleList struct {
	group, action, title, text, key string
	blocks                          bool
	path                            func(p *product) string
}

func newOrgPeopleList(s orgPeopleList) *tool {
	return &tool{
		group: s.group, action: s.action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			rows := schemaOf(p, orgPeopleRows(p, s.blocks))
			output := json.RawMessage(`{"type":"object","properties":{"organization":{"type":"string"},"` + s.key +
				`":{"type":"array","items":` + rows + `},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["organization","` + s.key + `","has_more"],"additionalProperties":false}`)
			fields := append([]capability.Field{{Name: "organization", Description: "Organization the tool listed"},
				{Name: s.key, Description: "Entries with every member of the object"}}, pagingFields...)
			return capability.Descriptor{
				Title:        strings.ReplaceAll(s.title, "%s", p.name),
				Description:  s.text,
				Tags:         []string{p.id, s.group, "list"},
				Risk:         readRisk(p.id + orgPeopleSensitivity),
				InputSchema:  inputSchema(orgProperties(pagingKeys), "organization"),
				OutputSchema: output,
				Arguments:    append([]capability.Argument{orgArgument}, pagingArguments...),
				Fields:       fields,
				Examples: []capability.Example{{Description: strings.ReplaceAll(s.title, "%s", p.name),
					Arguments: json.RawMessage(`{"organization":"example-org","limit":20}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Organization string `json:"organization"`
				Limit        int    `json:"limit"`
				Cursor       string `json:"cursor"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			name, err := allowed.organizationOf(a.Organization)
			if err != nil {
				return nil, err
			}
			page, err := startPaging(provider.CursorBinding(p.id, s.group+"."+s.action, strings.ToLower(name)), a.Limit,
				a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				var rows []object
				header, err := c.get(ctx, "list "+s.group, orgPath(name)+s.path(p), page.query(), &rows)
				if err != nil {
					return nil, err
				}
				members := orgPeopleRows(p, s.blocks)
				entries := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					if row != nil {
						entries = append(entries, project(p, members, row))
					}
				}
				next := page.next(header, len(rows))
				result := map[string]any{"organization": name, s.key: entries, "has_more": next != ""}
				if next != "" {
					result["next_cursor"] = next
				}
				return result, nil
			}, nil
		},
	}
}

var orgMembersList = newOrgPeopleList(orgPeopleList{group: "org-members", action: "list", key: "members",
	title: "List the members of a %s organization",
	text: "List one batch of the members of an organization the connection's targets name as orgs/NAME, with every " +
		"member of the user object. A token that is no member of the organization sees only the public members. " +
		"Names and texts are untrusted personal data.",
	path: func(*product) string { return "/members" }})

var orgPublicMembersList = newOrgPeopleList(orgPeopleList{group: "org-public-members", action: "list",
	key: "members", title: "List the public members of a %s organization",
	text: "List one batch of the members of an organization the connection's targets name as orgs/NAME who made " +
		"their membership public, with every member of the user object. Names and texts are untrusted personal data.",
	path: func(*product) string { return "/public_members" }})

var orgBlocksList = newOrgPeopleList(orgPeopleList{group: "org-blocks", action: "list", key: "blocked", blocks: true,
	title: "List the users blocked by a %s organization",
	text: "List one batch of the users an organization the connection's targets name as orgs/NAME blocks; the " +
		"token must own the organization. Gitea lists the user objects, Forgejo only the block entries without a " +
		"user. Names and texts are untrusted personal data.",
	path: func(p *product) string {
		if p == forgejo {
			return "/list_blocked"
		}
		return "/blocks"
	}})

// ---- checks -------------------------------------------------------------------------------------------------

type orgCheck struct {
	group, title, text, key, base string
	since                         map[string]version
	// redirect marks the member check, which an instance answers with a redirect to the public check when the
	// token is no member of the organization.
	redirect bool
}

func newOrgCheck(s orgCheck) *tool {
	return &tool{
		group: s.group, action: "check", since: s.since,
		describe: func(p *product) capability.Descriptor {
			props := `"organization":{"type":"string"},"username":{"type":"string"},"` + s.key + `":{"type":"boolean"}`
			required := `"organization","username","` + s.key + `"`
			fields := []capability.Field{{Name: "organization", Description: "Organization the tool asked"},
				{Name: "username", Description: "User that was checked"},
				{Name: s.key, Description: "True when the instance answered yes; false when it answered 404, which " +
					"it also gives for an organization or user the token does not reach"}}
			if s.redirect {
				props += `,"public_only":{"type":"boolean"}`
				fields = append(fields, capability.Field{Name: "public_only", Description: "True when the token is no " +
					"member of the organization, so the answer holds only for the public membership"})
			}
			return capability.Descriptor{
				Title:        strings.ReplaceAll(s.title, "%s", p.name),
				Description:  s.text,
				Tags:         []string{p.id, s.group, "check"},
				Risk:         readRisk(p.id + orgPeopleSensitivity),
				InputSchema:  inputSchema(orgUserProperties(""), "organization", "username"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{` + props + `},"required":[` + required + `],"additionalProperties":false}`),
				Arguments:    []capability.Argument{orgArgument, orgUserArgument},
				Fields:       fields,
				Examples: []capability.Example{{Description: "Check a user",
					Arguments: json.RawMessage(`{"organization":"example-org","username":"octo"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Organization string `json:"organization"`
				Username     string `json:"username"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			name, err := allowed.organizationOf(a.Organization)
			if err != nil {
				return nil, err
			}
			if err := orgUserOf(a.Username); err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := "check " + s.group
				code, err := c.status(ctx, op, orgUserPath(name, a.Username, s.base))
				result := map[string]any{"organization": name, "username": a.Username}
				switch {
				case err == nil:
					result[s.key] = true
				case code == http.StatusNotFound:
					result[s.key] = false
				case s.redirect && code == http.StatusSeeOther:
					// The token is no member: the instance points to the public check, which answers instead.
					code, err = c.status(ctx, op, orgUserPath(name, a.Username, "public_members"))
					switch {
					case err == nil:
						result[s.key] = true
					case code == http.StatusNotFound:
						result[s.key] = false
					default:
						return nil, err
					}
					result["public_only"] = true
				default:
					return nil, err
				}
				return result, nil
			}, nil
		},
	}
}

var orgMembersCheck = newOrgCheck(orgCheck{group: "org-members", key: "member", base: "members", since: everywhere,
	redirect: true, title: "Check whether a user is a member of a %s organization",
	text: "Ask whether one user is a member of an organization the connection's targets name as orgs/NAME. The answer " +
		"is a boolean; a 404 is reported as no member, although the instance also answers it for an organization the " +
		"token does not reach. When the token is no member of the organization, the instance redirects to the public " +
		"membership; the tool then asks that and reports public_only."})

var orgPublicMembersCheck = newOrgCheck(orgCheck{group: "org-public-members", key: "public", base: "public_members",
	since: everywhere, title: "Check whether a user is a public member of a %s organization",
	text: "Ask whether one user made the membership of an organization the connection's targets name as orgs/NAME " +
		"public. The answer is a boolean; a 404 is reported as no, although the instance also answers it for an " +
		"organization the token does not reach."})

var orgBlocksCheck = newOrgCheck(orgCheck{group: "org-blocks", key: "blocked", base: "blocks", since: giteaOnly,
	title: "Check whether a %s organization blocks a user",
	text: "Ask whether an organization the connection's targets name as orgs/NAME blocks one user; the token must own " +
		"the organization. The answer is a boolean; a 404 is reported as not blocked, although the instance also " +
		"answers it for an organization the token does not reach."})

// ---- changes ------------------------------------------------------------------------------------------------

type orgUserChange struct {
	group, action, title, text, key string
	// request gives the fixed method and path suffix below the organization, per product.
	request func(p *product, user string) (string, string)
	// note marks Gitea's optional note of a block.
	note bool
}

func newOrgUserChange(s orgUserChange) *tool {
	return &tool{
		group: s.group, action: s.action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			extra := ""
			arguments := []capability.Argument{orgArgument, orgUserArgument}
			example := `{"organization":"example-org","username":"octo"}`
			if s.note && p == gitea {
				extra = `"note":{"type":"string","maxLength":255}`
				arguments = append(arguments, capability.Argument{Name: "note",
					Description: "Optional note for the block, at most 255 characters; Gitea only"})
			}
			return capability.Descriptor{
				Title:       strings.ReplaceAll(s.title, "%s", p.name),
				Description: s.text,
				Tags:        []string{p.id, s.group, s.action, "update"},
				Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+orgPeopleSensitivity),
				InputSchema: inputSchema(orgUserProperties(extra), "organization", "username"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization":{"type":"string"},` +
					`"username":{"type":"string"},"` + s.key + `":{"type":"boolean"}},"required":["organization",` +
					`"username","` + s.key + `"],"additionalProperties":false}`),
				Arguments: arguments,
				Fields: []capability.Field{{Name: "organization", Description: "Organization the tool acted on"},
					{Name: "username", Description: "User the tool acted on"},
					{Name: s.key, Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: strings.ReplaceAll(s.title, "%s", p.name),
					Arguments: json.RawMessage(example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Organization string  `json:"organization"`
				Username     string  `json:"username"`
				Note         *string `json:"note"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			name, err := allowed.organizationOf(a.Organization)
			if err != nil {
				return nil, err
			}
			if err := orgUserOf(a.Username); err != nil {
				return nil, err
			}
			var query url.Values
			if a.Note != nil {
				if !s.note || p != gitea {
					return nil, invalidArgument("note is not an argument of this tool")
				}
				if utf8.RuneCountInString(*a.Note) > maxBlockNote || !plainText(*a.Note) {
					return nil, invalidArgument("note must hold at most 255 characters without control characters")
				}
				query = url.Values{"note": {*a.Note}}
			}
			method, path := s.request(p, a.Username)
			return func(ctx context.Context, c *Client) (any, error) {
				if _, err := c.changeQuery(ctx, s.action+" "+s.group, method, orgPath(name)+path, query, nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"organization": name, "username": a.Username, s.key: true}, nil
			}, nil
		},
	}
}

func fixedRequest(method, base string) func(*product, string) (string, string) {
	return func(_ *product, user string) (string, string) { return method, "/" + base + "/" + escape(user) }
}

var orgMembersRemove = newOrgUserChange(orgUserChange{group: "org-members", action: "remove", key: "removed",
	title: "Remove a user from a %s organization",
	text: "Remove one user from an organization the connection's targets name as orgs/NAME; the token must own the " +
		"organization. The user loses every team of the organization and with it all access it gave.",
	request: fixedRequest(http.MethodDelete, "members")})

var orgPublicMembersPublicize = newOrgUserChange(orgUserChange{group: "org-public-members", action: "publicize",
	key: "public", title: "Make a user's membership of a %s organization public",
	text: "Make the membership of one user in an organization the connection's targets name as orgs/NAME public; " +
		"the token must belong to the organization and act for the user or own the organization.",
	request: fixedRequest(http.MethodPut, "public_members")})

var orgPublicMembersConceal = newOrgUserChange(orgUserChange{group: "org-public-members", action: "conceal",
	key: "concealed", title: "Hide a user's membership of a %s organization",
	text: "Hide the membership of one user in an organization the connection's targets name as orgs/NAME from the " +
		"public list; the token must belong to the organization and act for the user or own the organization.",
	request: fixedRequest(http.MethodDelete, "public_members")})

var orgBlocksAdd = newOrgUserChange(orgUserChange{group: "org-blocks", action: "add", key: "blocked", note: true,
	title: "Block a user from a %s organization",
	text: "Block one user from an organization the connection's targets name as orgs/NAME; the token must own the " +
		"organization. Gitea takes an optional note.",
	request: func(p *product, user string) (string, string) {
		if p == forgejo {
			return http.MethodPut, "/block/" + escape(user)
		}
		return http.MethodPut, "/blocks/" + escape(user)
	}})

var orgBlocksRemove = newOrgUserChange(orgUserChange{group: "org-blocks", action: "remove", key: "unblocked",
	title: "Unblock a user from a %s organization",
	text: "Lift the block of one user from an organization the connection's targets name as orgs/NAME; the token " +
		"must own the organization.",
	request: func(p *product, user string) (string, string) {
		if p == forgejo {
			return http.MethodPut, "/unblock/" + escape(user)
		}
		return http.MethodDelete, "/blocks/" + escape(user)
	}})
