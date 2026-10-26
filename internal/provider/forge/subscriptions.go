package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The subscription tools act on the subscribers of one issue or pull request of a repository inside the
// connection's targets. Every call first proves by the evidence read of the index that its canonical repository
// lies inside the targets. A change names an existing user as one validated path segment and sends exactly one
// request; the instance answers 201 for a state it changed and 200 for one that already held, and 304 when the
// account is no administrator and names another user.

var subscriptionCheckMembers = []member{
	{name: "subscribed", kind: kindFlag, note: "True when the account behind the token is subscribed"},
	{name: "ignored", kind: kindFlag, note: "True when notifications are ignored"},
	{name: "reason", kind: kindText, note: "Reason of the subscription state; untrusted data"},
	{name: "created_at", kind: kindText, note: "Time the subscription was created"},
	{name: "url", kind: kindText, address: true, note: "Address for managing the subscription"},
	{name: "repository_url", kind: kindText, address: true, note: "Address of the repository"},
}

var subscriptionUserArgument = capability.Argument{Name: "user", Required: true,
	Description: "User name of an existing user of the instance; an account that is no administrator can name only itself"}

type subscriptionCall struct {
	Repository string `json:"repository"`
	Number     int64  `json:"number"`
	User       string `json:"user"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor"`
}

func subscriptionsPath(repo target, number int64) string {
	return issuePath(repo, number) + "/subscriptions"
}

func subscriptionTarget(allowed allowlist, a subscriptionCall, needUser bool) (target, error) {
	repo, err := allowed.chooseRepository(a.Repository)
	if err != nil {
		return target{}, err
	}
	if a.Number < 1 {
		return target{}, invalidArgument("number must be the index of an issue or pull request")
	}
	if needUser && (a.User == "" || len(a.User) > 40 || !validName(a.User)) {
		return target{}, invalidArgument("user must be a user name")
	}
	return repo, nil
}

func subscriptionBase(group, action, title, text string, p *product, risk capability.Risk, user bool,
	output, example string, fields []capability.Field) capability.Descriptor {
	properties, required := `"repository":`+repoSchema+`,"number":`+issueSchemaNumber, []string{"number"}
	arguments := []capability.Argument{repositoryArgument, numberArgument}
	if user {
		properties += `,"user":` + issueSchemaLogin
		required = append(required, "user")
		arguments = append(arguments, subscriptionUserArgument)
	}
	return capability.Descriptor{
		Title:        title + " " + p.name + " issue or pull request",
		Description:  text,
		Tags:         []string{p.id, group, action},
		Risk:         risk,
		InputSchema:  inputSchema(properties, required...),
		OutputSchema: json.RawMessage(output),
		Arguments:    arguments,
		Fields:       fields,
		Examples:     []capability.Example{{Description: title + " issue 7", Arguments: json.RawMessage(example)}},
	}
}

var issueSubscriptionsList = &tool{
	group: "issue-subscriptions", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		d := subscriptionBase("issue-subscriptions", "list", "List the subscribers of a",
			"List one batch of the users subscribed to an issue or pull request of a repository inside the "+
				"connection's targets, with every member of each user; the index is read first. Names and texts are "+
				"untrusted personal data.", p, readRisk(p.id+issueSensitivity), false,
			`{"type":"object","properties":{"users":{"type":"array","items":`+schemaOf(p, personMembers, "id", "login")+
				`},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},"required":["users"],"additionalProperties":false}`,
			`{"repository":"example/project","number":7}`,
			append([]capability.Field{{Name: "users", Description: "Subscribers with every member of the user object"}},
				pagingFields...))
		d.InputSchema = inputSchema(`"repository":`+repoSchema+`,"number":`+issueSchemaNumber+`,`+pagingKeys, "number")
		d.Arguments = append(d.Arguments, pagingArguments...)
		return d
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a subscriptionCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := subscriptionTarget(allowed, a, false)
		if err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "issue-subscriptions.list",
			strings.ToLower(repo.owner+"/"+repo.repo), a.Number), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := readIndex(ctx, c, allowed, "list subscribers", repo, a.Number); err != nil {
				return nil, err
			}
			var rows []object
			header, err := c.get(ctx, "list subscribers", subscriptionsPath(repo, a.Number), page.query(), &rows)
			if err != nil {
				return nil, err
			}
			users := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					users = append(users, project(p, personMembers, row))
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"users": users, "has_more": next != ""}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

var issueSubscriptionsCheck = &tool{
	group: "issue-subscriptions", action: "check", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return subscriptionBase("issue-subscriptions", "check", "Check the own subscription to a",
			"Ask whether the account behind the token is subscribed to an issue or pull request of a repository "+
				"inside the connection's targets, with every member of the answer; the index is read first.",
			p, readRisk(p.id+issueSensitivity), false, schemaOf(p, subscriptionCheckMembers, "subscribed"),
			`{"repository":"example/project","number":7}`, fieldsOf(p, subscriptionCheckMembers))
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a subscriptionCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := subscriptionTarget(allowed, a, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := readIndex(ctx, c, allowed, "check subscription", repo, a.Number); err != nil {
				return nil, err
			}
			var answer object
			if _, err := c.get(ctx, "check subscription", subscriptionsPath(repo, a.Number)+"/check", nil, &answer); err != nil {
				return nil, err
			}
			return project(p, subscriptionCheckMembers, answer), nil
		}, nil
	},
}

// subscriptionChange builds the subscribe or unsubscribe of one user.
func subscriptionChange(action, method, title, done string) *tool {
	return &tool{
		group: "issue-subscriptions", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			verb := map[string]string{"add": "Subscribe a user to", "remove": "Unsubscribe a user from"}[action]
			return subscriptionBase("issue-subscriptions", action, title,
				verb+" an issue or pull request of a repository inside the connection's targets; the index is read "+
					"first. The instance accepts another user only from an administrator and otherwise refuses with "+
					"304; "+done+" false tells that the state already held.",
				p, changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+issueSensitivity), true,
				`{"type":"object","properties":{"number":{"type":"integer"},"user":{"type":"string"},"`+done+
					`":{"type":"boolean"}},"required":["number","user","`+done+`"],"additionalProperties":false}`,
				`{"repository":"example/project","number":7,"user":"alice"}`,
				[]capability.Field{{Name: "number", Description: "The issue or pull request the call acted on"},
					{Name: "user", Description: "The user the call acted on"},
					{Name: done, Description: "True when the instance changed the state (201); false when it already held (200)"}})
		},
		prepare: func(_ *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a subscriptionCall
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			repo, err := subscriptionTarget(allowed, a, true)
			if err != nil {
				return nil, err
			}
			op := action + " subscription"
			return func(ctx context.Context, c *Client) (any, error) {
				if err := readIndex(ctx, c, allowed, op, repo, a.Number); err != nil {
					return nil, err
				}
				status, err := c.changeQuery(ctx, op, method, subscriptionsPath(repo, a.Number)+"/"+escape(a.User), nil, nil, nil)
				if status == http.StatusNotModified {
					return nil, keepOriginal(provider.Fail(op, "the account behind the token is no administrator and "+
						"can change only its own subscription"), err)
				}
				if err != nil {
					return nil, err
				}
				return map[string]any{"number": a.Number, "user": a.User, done: status == http.StatusCreated}, nil
			}, nil
		},
	}
}

var (
	issueSubscriptionsAdd    = subscriptionChange("add", http.MethodPut, "Subscribe a user to a", "added")
	issueSubscriptionsRemove = subscriptionChange("remove", http.MethodDelete, "Unsubscribe a user from a", "removed")
)
