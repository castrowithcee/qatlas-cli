package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The user tools. A view of another person reaches past any target, so it is offered only without targets and
// refused before any request otherwise. A view of the account behind the token (the /user paths) is bound to
// that account: with targets, the connection must name it as users/NAME, which a read of the account proves,
// and a repository target refuses it. The user objects keep the person members only; the account's security
// members stay out. Names and texts are untrusted data and the profile addresses are never fetched.

const (
	userSensitivity = "-personal-data"
	maxHeatmap      = 400
)

var userSortValues = `{"type":"string","enum":["oldest","newest","alphabetically","reversealphabetically",` +
	`"recentupdate","leastupdate"]}`

var userSortNames = map[string]bool{"oldest": true, "newest": true, "alphabetically": true,
	"reversealphabetically": true, "recentupdate": true, "leastupdate": true}

var heatmapMembers = []member{
	{name: "timestamp", kind: kindInteger, note: "Start of the day as Unix time in seconds"},
	{name: "contributions", kind: kindInteger, note: "Number of contributions of that time"},
}

// userNameOf checks a user name argument without quoting it.
func userNameOf(field, value string) error {
	if !validName(value) {
		return invalidArgument(field + " must be a user name")
	}
	return nil
}

// otherPeopleOnly refuses a view of another person on a connection with targets, before any request, without
// naming the person.
func (a allowlist) otherPeopleOnly() error {
	if a.bound() {
		return invalidArgument("users and their data reach past any target, so this connection's targets do not " +
			"allow reading them; use a connection without targets")
	}
	return nil
}

// ownAccountOnly allows a view of the account behind the token: without targets, or with a users/NAME target
// (proved by verifyOwnAccount at run time) and no repository target.
func (a allowlist) ownAccountOnly() error {
	if !a.bound() {
		return nil
	}
	if err := a.accountWide(); err != nil {
		return err
	}
	for _, entry := range a.targets {
		if entry.kind == kindOwner && entry.scope == "users" {
			return nil
		}
	}
	return invalidArgument("the account behind the token is reached only when the connection's targets name it as " +
		"users/NAME; add that target or use a connection without targets")
}

func userRows(p *product, rows []object) []map[string]any {
	users := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			users = append(users, project(p, personMembers, row))
		}
	}
	return users
}

func userListOutput(p *product, extra string) json.RawMessage {
	if extra != "" {
		extra += ","
	}
	return json.RawMessage(`{"type":"object","properties":{"users":{"type":"array","items":` +
		schemaOf(p, personMembers) + `},` + extra + `"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["users","has_more"],"additionalProperties":false}`)
}

func pageResult(page paging, header http.Header, rows int, key string, list any) map[string]any {
	next := page.next(header, rows)
	result := map[string]any{key: list, "has_more": next != ""}
	if next != "" {
		result["next_cursor"] = next
	}
	return result
}

// ---- search and get -----------------------------------------------------------------------------------------

var usersSearch = &tool{
	group: "users", action: "search", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"q":` + searchSchemaQ + `,"uid":` + searchSchemaID
		arguments := []capability.Argument{{Name: "q", Description: "Keyword in the user name, display name, or " +
			"email address the instance matches; all visible users when omitted"},
			{Name: "uid", Description: "Only the user with this numeric ID"}}
		if p == forgejo {
			properties += `,"sort":` + userSortValues
			arguments = append(arguments, capability.Argument{Name: "sort", Description: "Forgejo only: oldest, " +
				"newest, alphabetically, reversealphabetically, recentupdate, or leastupdate"})
		}
		return capability.Descriptor{
			Title: "Search " + p.name + " users",
			Description: "Search the users of the whole instance and return one batch with the person members of " +
				"each user object. The search is instance-wide, so only a connection without targets offers this " +
				"tool; one with targets refuses it before any request. Names and texts are untrusted personal data.",
			Tags:         []string{p.id, "users", "search"},
			Risk:         readRisk(p.id + userSensitivity),
			InputSchema:  inputSchema(properties + `,` + pagingKeys),
			OutputSchema: userListOutput(p, ""),
			Arguments:    append(arguments, pagingArguments...),
			Fields: append([]capability.Field{{Name: "users", Description: "Users with the person members of the " +
				"user object"}}, pagingFields...),
			Examples: []capability.Example{{Description: "Search users by keyword",
				Arguments: json.RawMessage(`{"q":"octo","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.otherPeopleOnly(); err != nil {
			return nil, err
		}
		var a struct {
			Q      string `json:"q"`
			UID    int64  `json:"uid"`
			Sort   string `json:"sort"`
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		filters := url.Values{}
		if a.Q != "" {
			if len(a.Q) > maxQuery || !plainText(a.Q) {
				return nil, invalidArgument("q must be plain text of at most 256 bytes")
			}
			filters.Set("q", a.Q)
		}
		if a.UID != 0 {
			if a.UID < 1 {
				return nil, invalidArgument("uid must be a positive user ID")
			}
			filters.Set("uid", itoa64(a.UID))
		}
		if a.Sort != "" {
			if p != forgejo {
				return nil, invalidArgument("sort is a Forgejo filter; " + p.name + " does not know it")
			}
			if !userSortNames[a.Sort] {
				return nil, invalidArgument("sort is not one of the documented orders")
			}
			filters.Set("sort", a.Sort)
		}
		page, err := startPaging(provider.CursorBinding(p.id, "users.search", "", "", filters.Encode()), a.Limit,
			a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "search users"
			query := page.query()
			for name, values := range filters {
				query[name] = values
			}
			var answer json.RawMessage
			header, err := c.get(ctx, op, "/users/search", query, &answer)
			if err != nil {
				return nil, err
			}
			entries, err := unwrapList(op, p.name, answer, true)
			if err != nil {
				return nil, err
			}
			rows := make([]object, 0, len(entries))
			for _, entry := range entries {
				var row object
				if json.Unmarshal(entry, &row) != nil {
					return nil, provider.InvalidResponse(op, p.name+" returned an invalid list")
				}
				rows = append(rows, row)
			}
			return pageResult(page, header, len(rows), "users", userRows(p, rows)), nil
		}, nil
	},
}

var usersGet = &tool{
	group: "users", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " user",
			Description: "Read one user by name with the person members of the user object. It reaches past any " +
				"target, so only a connection without targets offers it. Names and texts are untrusted personal data.",
			Tags:         []string{p.id, "users", "get"},
			Risk:         readRisk(p.id + userSensitivity),
			InputSchema:  inputSchema(`"username":`+issueSchemaLogin, "username"),
			OutputSchema: json.RawMessage(schemaOf(p, personMembers, "id", "login")),
			Arguments:    []capability.Argument{{Name: "username", Required: true, Description: "User name"}},
			Fields:       fieldsOf(p, personMembers),
			Examples: []capability.Example{{Description: "Read a user",
				Arguments: json.RawMessage(`{"username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.otherPeopleOnly(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var user object
			if _, err := c.get(ctx, "get user", "/users/"+escape(a.Username), nil, &user); err != nil {
				return nil, err
			}
			return project(p, personMembers, user), nil
		}, nil
	},
}

// ---- activities and heatmap ---------------------------------------------------------------------------------

var userActivitiesList = &tool{
	group: "user-activities", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the " + p.name + " activities of a user",
			Description: "List one batch of the activity feed of one user with every member of each activity. It " +
				"reaches past any target, so only a connection without targets offers it. The feed names the users " +
				"who acted, and its texts are untrusted data.",
			Tags: []string{p.id, "user-activities", "list"},
			Risk: readRisk(p.id + activitySensitivity),
			InputSchema: inputSchema(`"username":`+issueSchemaLogin+`,"only_performed_by":`+searchSchemaFlag+
				`,"date":`+activitySchemaDt+`,`+pagingKeys, "username"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"activities":{"type":"array","items":` +
				schemaOf(p, activityMembers) + `},"username":{"type":"string"},"next_cursor":{"type":"string"},` +
				`"has_more":{"type":"boolean"}},"required":["activities","username","has_more"],` +
				`"additionalProperties":false}`),
			Arguments: append([]capability.Argument{{Name: "username", Required: true, Description: "User name"},
				{Name: "only_performed_by", Description: "True to show only actions the user performed"},
				{Name: "date", Description: "Only activities of this day, YYYY-MM-DD"}}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "activities", Description: "Activities with every member of " +
				"the activity object"}, {Name: "username", Description: "User the tool listed"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the activities of a day",
				Arguments: json.RawMessage(`{"username":"octo","date":"2030-01-02"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.otherPeopleOnly(); err != nil {
			return nil, err
		}
		var a struct {
			Username        string `json:"username"`
			OnlyPerformedBy *bool  `json:"only_performed_by"`
			Date            string `json:"date"`
			Limit           int    `json:"limit"`
			Cursor          string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		filters := url.Values{}
		if a.OnlyPerformedBy != nil {
			filters.Set("only-performed-by", boolText(*a.OnlyPerformedBy))
		}
		if a.Date != "" {
			if _, err := time.Parse("2006-01-02", a.Date); err != nil {
				return nil, invalidArgument("date must be a day as YYYY-MM-DD")
			}
			filters.Set("date", a.Date)
		}
		page, err := startPaging(provider.CursorBinding(p.id, "user-activities.list", strings.ToLower(a.Username), "",
			filters.Encode()), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			for name, values := range filters {
				query[name] = values
			}
			var rows []object
			header, err := c.get(ctx, "list user activities", "/users/"+escape(a.Username)+"/activities/feeds", query,
				&rows)
			if err != nil {
				return nil, err
			}
			activities := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					activities = append(activities, project(p, activityMembers, row))
				}
			}
			result := pageResult(page, header, len(rows), "activities", activities)
			result["username"] = a.Username
			return result, nil
		}, nil
	},
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

var userHeatmapGet = &tool{
	group: "user-heatmap", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the " + p.name + " contribution heatmap of a user",
			Description: "Read the contribution counts of one user per time stamp, at most 400 entries. It reaches " +
				"past any target, so only a connection without targets offers it. An instance can switch the " +
				"heatmap off, and then it answers as if the user did not exist.",
			Tags:        []string{p.id, "user-heatmap", "get"},
			Risk:        readRisk(p.id + userSensitivity),
			InputSchema: inputSchema(`"username":`+issueSchemaLogin, "username"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"heatmap":{"type":"array","items":` +
				schemaOf(p, heatmapMembers) + `},"username":{"type":"string"},"truncated":{"type":"boolean"}},` +
				`"required":["heatmap","username","truncated"],"additionalProperties":false}`),
			Arguments: []capability.Argument{{Name: "username", Required: true, Description: "User name"}},
			Fields: []capability.Field{{Name: "heatmap", Description: "Entries with timestamp and contributions"},
				{Name: "username", Description: "User the tool read"},
				{Name: "truncated", Description: "True when the instance sent more than 400 entries"}},
			Examples: []capability.Example{{Description: "Read a heatmap", Arguments: json.RawMessage(`{"username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.otherPeopleOnly(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if _, err := c.get(ctx, "get user heatmap", "/users/"+escape(a.Username)+"/heatmap", nil, &rows); err != nil {
				return nil, err
			}
			truncated := len(rows) > maxHeatmap
			if truncated {
				rows = rows[:maxHeatmap]
			}
			entries := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					entries = append(entries, project(p, heatmapMembers, row))
				}
			}
			return map[string]any{"heatmap": entries, "username": a.Username, "truncated": truncated}, nil
		}, nil
	},
}

// ---- followers and following --------------------------------------------------------------------------------

var userNameProperty = `"username":` + issueSchemaLogin

func newFollowList(action, noun, path string) *tool {
	return &tool{
		group: "user-" + noun, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "List the " + p.name + " " + noun + " of a user",
				Description: "List one batch of the " + noun + " of one user, or of the account behind the token when " +
					"username is left out, with the person members of each user object. With username it reaches past " +
					"any target, so only a connection without targets offers it. Without username, targets must " +
					"name the account as users/NAME. The listed people are untrusted personal data.",
				Tags:         []string{p.id, "user-" + noun, "list"},
				Risk:         readRisk(p.id + userSensitivity),
				InputSchema:  inputSchema(userNameProperty + `,` + pagingKeys),
				OutputSchema: userListOutput(p, `"username":{"type":"string"}`),
				Arguments: append([]capability.Argument{{Name: "username", Description: "User whose " + noun +
					" to list; the account behind the token when omitted"}}, pagingArguments...),
				Fields: append([]capability.Field{{Name: "users", Description: "Users with the person members of " +
					"the user object"}, {Name: "username", Description: "User the tool listed, absent for the account " +
					"behind the token"}}, pagingFields...),
				Examples: []capability.Example{{Description: "List the " + noun + " of a user",
					Arguments: json.RawMessage(`{"username":"octo"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Username string `json:"username"`
				Limit    int    `json:"limit"`
				Cursor   string `json:"cursor"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			base := "/user/" + path
			if a.Username != "" {
				if err := allowed.otherPeopleOnly(); err != nil {
					return nil, err
				}
				if err := userNameOf("username", a.Username); err != nil {
					return nil, err
				}
				base = "/users/" + escape(a.Username) + "/" + path
			} else if err := allowed.ownAccountOnly(); err != nil {
				return nil, err
			}
			page, err := startPaging(provider.CursorBinding(p.id, "user-"+noun+".list", strings.ToLower(a.Username)),
				a.Limit, a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				const op = "list users"
				if a.Username == "" {
					if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
						return nil, err
					}
				}
				var rows []object
				header, err := c.get(ctx, op, base, page.query(), &rows)
				if err != nil {
					return nil, err
				}
				result := pageResult(page, header, len(rows), "users", userRows(p, rows))
				if a.Username != "" {
					result["username"] = a.Username
				}
				return result, nil
			}, nil
		},
	}
}

var userFollowersList = newFollowList("list", "followers", "followers")
var userFollowingList = newFollowList("list", "following", "following")

var userFollowingCheck = &tool{
	group: "user-following", action: "check", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether a " + p.name + " user follows another",
			Description: "Ask whether a user follows the user named by target; without username the user is the " +
				"account behind the token. The answer is a boolean; a 404 is reported as no, although the instance " +
				"also answers it for a user the token does not reach. With username it reaches past any target, so " +
				"only a connection without targets offers it; without username, targets must name the account as " +
				"users/NAME.",
			Tags:        []string{p.id, "user-following", "check"},
			Risk:        readRisk(p.id + userSensitivity),
			InputSchema: inputSchema(userNameProperty+`,"target":`+issueSchemaLogin, "target"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"username":{"type":"string"},` +
				`"target":{"type":"string"},"following":{"type":"boolean"}},"required":["target","following"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{{Name: "username", Description: "User who may follow; the account " +
				"behind the token when omitted"}, {Name: "target", Required: true, Description: "User name of the " +
				"possibly followed user"}},
			Fields: []capability.Field{{Name: "username", Description: "User that was asked, absent for the account " +
				"behind the token"}, {Name: "target", Description: "User that was checked"},
				{Name: "following", Description: "True when the instance answered yes; false when it answered 404"}},
			Examples: []capability.Example{{Description: "Check a follow",
				Arguments: json.RawMessage(`{"username":"octo","target":"hub"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Username string `json:"username"`
			Target   string `json:"target"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("target", a.Target); err != nil {
			return nil, err
		}
		path := "/user/following/" + escape(a.Target)
		if a.Username != "" {
			if err := allowed.otherPeopleOnly(); err != nil {
				return nil, err
			}
			if err := userNameOf("username", a.Username); err != nil {
				return nil, err
			}
			path = "/users/" + escape(a.Username) + "/following/" + escape(a.Target)
		} else if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "check following"
			if a.Username == "" {
				if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
					return nil, err
				}
			}
			code, err := c.status(ctx, op, path)
			result := map[string]any{"target": a.Target}
			if a.Username != "" {
				result["username"] = a.Username
			}
			switch {
			case err == nil:
				result["following"] = true
			case code == http.StatusNotFound:
				result["following"] = false
			default:
				return nil, err
			}
			return result, nil
		}, nil
	},
}

func newFollowChange(action, key, method, title, text string) *tool {
	return &tool{
		group: "user-follows", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title:       strings.ReplaceAll(title, "%s", p.name),
				Description: text,
				Tags:        []string{p.id, "user-follows", action, "update"},
				Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+userSensitivity),
				InputSchema: inputSchema(userNameProperty, "username"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"username":{"type":"string"},"` + key +
					`":{"type":"boolean"}},"required":["username","` + key + `"],"additionalProperties":false}`),
				Arguments: []capability.Argument{{Name: "username", Required: true, Description: "User name of the " +
					"person"}},
				Fields: []capability.Field{{Name: "username", Description: "User the tool acted on"},
					{Name: key, Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: strings.ReplaceAll(title, "%s", p.name),
					Arguments: json.RawMessage(`{"username":"octo"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Username string `json:"username"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			if err := userNameOf("username", a.Username); err != nil {
				return nil, err
			}
			if err := allowed.ownAccountOnly(); err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				if err := verifyOwnAccount(ctx, c, action+" follow", allowed); err != nil {
					return nil, err
				}
				if err := c.change(ctx, action+" follow", method, "/user/following/"+escape(a.Username), nil,
					nil); err != nil {
					return nil, err
				}
				return map[string]any{"username": a.Username, key: true}, nil
			}, nil
		},
	}
}

var userFollowsAdd = newFollowChange("add", "followed", http.MethodPut, "Follow a user on %s",
	"Make the account behind the token follow one user. With targets, a read of the account first proves that they "+
		"name it as users/NAME. The change is the one request.")

var userFollowsRemove = newFollowChange("remove", "unfollowed", http.MethodDelete, "Unfollow a user on %s",
	"Make the account behind the token stop following one user. With targets, a read of the account first proves "+
		"that they name it as users/NAME. The change is the one request.")
