package forge

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The watch tools read and change the watch state of the account behind the token on one repository inside the
// connection's targets. The instance's watch object is the one of the issue subscription check; the API has no
// option to ignore a repository, so setting a watch sends no body.

func watchPath(repo target) string { return repositoryPath(repo) + "/subscription" }

func watchOutput(extra string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` + extra +
		`},"required":["repository"],"additionalProperties":false}`)
}

func watchProperties(p *product) string {
	var out string
	for _, m := range subscriptionCheckMembers {
		out += `"` + m.name + `":` + m.schema(p) + `,`
	}
	return out[:len(out)-1]
}

func watchResult(p *product, repo target, answer object) map[string]any {
	result := project(p, subscriptionCheckMembers, answer)
	result["repository"] = fullName(repo)
	return result
}

var repositorySubscriptionsGet = &tool{
	group: "repository-subscriptions", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the watch state of a " + p.name + " repository",
			Description: "Read whether the account behind the token watches a repository inside the connection's " +
				"targets, with every member of the watch object. A 404 is reported as not watching, although the " +
				"instance also answers it for a repository the token does not reach.",
			Tags:         []string{p.id, "repository-subscriptions", "get"},
			Risk:         readRisk(p.id + repositorySensitivity),
			InputSchema:  inputSchema(`"repository":` + repoSchema),
			OutputSchema: watchOutput(watchProperties(p)),
			Arguments:    []capability.Argument{repositoryArgument},
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository the tool asked, as OWNER/REPO"}},
				fieldsOf(p, subscriptionCheckMembers)...),
			Examples: []capability.Example{{Description: "Read the watch state",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a repositoryStarCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get watch state"
			var answer object
			status, _, err := send(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, watchPath(repo), nil,
				&answer)
			if status == http.StatusNotFound {
				return map[string]any{"repository": fullName(repo), "subscribed": false}, nil
			}
			if err != nil {
				return nil, err
			}
			return watchResult(p, repo, answer), nil
		}, nil
	},
}

var repositorySubscriptionsSet = &tool{
	group: "repository-subscriptions", action: "set", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Watch a " + p.name + " repository",
			Description: "Make the account behind the token watch a repository inside the connection's targets and " +
				"return the watch object. Only the account behind the token changes. The change is the one request.",
			Tags:         []string{p.id, "repository-subscriptions", "set", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
			InputSchema:  inputSchema(`"repository":` + repoSchema),
			OutputSchema: watchOutput(watchProperties(p)),
			Arguments:    []capability.Argument{repositoryArgument},
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository the tool changed, as OWNER/REPO"}},
				fieldsOf(p, subscriptionCheckMembers)...),
			Examples: []capability.Example{{Description: "Watch a repository",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a repositoryStarCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			if err := c.change(ctx, "set watch state", http.MethodPut, watchPath(repo), nil, &answer); err != nil {
				return nil, err
			}
			return watchResult(p, repo, answer), nil
		}, nil
	},
}

var repositorySubscriptionsDelete = &tool{
	group: "repository-subscriptions", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Stop watching a " + p.name + " repository",
			Description: "Make the account behind the token stop watching a repository inside the connection's " +
				"targets. Only the account behind the token changes. The change is the one request.",
			Tags:        []string{p.id, "repository-subscriptions", "delete", "update"},
			Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"unsubscribed":{"type":"boolean"}},"required":["repository","unsubscribed"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository the tool changed, as OWNER/REPO"},
				{Name: "unsubscribed", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Stop watching a repository",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(_ *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a repositoryStarCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete watch state", http.MethodDelete, watchPath(repo), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "unsubscribed": true}, nil
		}, nil
	},
}
