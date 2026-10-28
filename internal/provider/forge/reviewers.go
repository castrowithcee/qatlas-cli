package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The reviewer tools read who can review in a repository inside the targets and ask or stop asking users and
// teams for a review of one pull request. The pull request is proven by the evidence read before a change.

var reviewersArgument = capability.Argument{Name: "reviewers", Description: "User names, at most 20; with " +
	"team_reviewers at least one name in all"}

var teamReviewersArgument = capability.Argument{Name: "team_reviewers", Description: "Team names of the " +
	"organization that owns the repository, at most 20; only when the connection's targets are empty or name " +
	"that organization as orgs/NAME"}

func reviewersPath(repo target) string {
	return "/repos/" + escape(repo.owner) + "/" + escape(repo.repo) + "/reviewers"
}

// teamsAllowed reports whether teams may be named: they belong to the organization that owns the repository,
// which the targets must then cover as a whole (or not restrict at all).
func (a allowlist) teamsAllowed(owner string) bool {
	if !a.bound() {
		return true
	}
	for _, entry := range a.targets {
		if entry.kind == kindOwner && entry.scope == "orgs" && strings.EqualFold(entry.owner, owner) {
			return true
		}
	}
	return false
}

var reviewersList = &tool{
	group: "pull-request-reviewers", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the possible reviewers of a " + p.name + " repository",
			Description: "List the users who can be asked to review the pull requests of a repository inside the " +
				"connection's targets, with every member of each user; names and texts are untrusted personal data.",
			Tags:        []string{p.id, "pull-request-reviewers", "list"},
			Risk:        readRisk(p.id + reviewSensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"reviewers":{"type":"array","items":` +
				schemaOf(p, personMembers, "id", "login") + `},"truncated":{"type":"boolean"}},` +
				`"required":["reviewers"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: []capability.Field{{Name: "reviewers", Description: "Users with every member of the user object"},
				{Name: "truncated", Description: "True when the instance sent more entries than the tool returns"}},
			Examples: []capability.Example{{Description: "List the possible reviewers",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if _, err := c.get(ctx, "list possible reviewers", reviewersPath(repo), nil, &rows); err != nil {
				return nil, err
			}
			result := map[string]any{}
			if len(rows) > maxListItems {
				rows, result["truncated"] = rows[:maxListItems], true
			}
			users := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					users = append(users, project(p, personMembers, row))
				}
			}
			result["reviewers"] = users
			return result, nil
		}, nil
	},
}

// reviewersChange is pull-request-reviewers.request and remove: one request with the validated names.
func reviewersChange(action, method, verb, preposition string, effect capability.Effect,
	idempotency capability.Idempotency) *tool {
	return &tool{
		group: "pull-request-reviewers", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			names := `{"type":"array","maxItems":20,"items":` + issueSchemaLogin + `}`
			output := `{"type":"object","properties":{"number":{"type":"integer"},"done":{"type":"boolean"}},` +
				`"required":["number","done"],"additionalProperties":false}`
			fields := []capability.Field{{Name: "number", Description: "Index of the pull request"},
				{Name: "done", Description: "True once the instance accepted the change"}}
			description := verb + " review requests " + preposition + " one pull request of a repository inside the " +
				"connection's targets for users and teams that exist on the instance; this takes back requests " +
				"only. The index of an issue is refused."
			if action == "request" {
				output = `{"type":"object","properties":{"number":{"type":"integer"},"reviews":{"type":"array",` +
					`"items":` + schemaOf(p, reviewMembers, "id") + `}},"required":["number","reviews"],` +
					`"additionalProperties":false}`
				fields = []capability.Field{{Name: "number", Description: "Index of the pull request"},
					{Name: "reviews", Description: "Reviews of the instance's answer, with every member of the review"}}
				description = "Ask users and teams for a review of one pull request of a repository inside the " +
					"connection's targets and return the review requests. The index of an issue is refused."
			}
			return capability.Descriptor{
				Title:       verb + " review requests " + preposition + " a " + p.name + " pull request",
				Description: description,
				Tags:        []string{p.id, "pull-request-reviewers", action},
				Risk:        changeRisk(effect, idempotency, p.id+reviewSensitivity),
				InputSchema: inputSchema(pullNumberProperties+`,"reviewers":`+names+`,"team_reviewers":`+names,
					"number"),
				OutputSchema: json.RawMessage(output),
				Arguments:    []capability.Argument{repositoryArgument, pullNumberArgument, reviewersArgument, teamReviewersArgument},
				Fields:       fields,
				Examples: []capability.Example{{Description: verb + " a reviewer",
					Arguments: json.RawMessage(`{"repository":"example/project","number":7,"reviewers":["alice"]}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments struct {
				Repository    string   `json:"repository"`
				Number        int64    `json:"number"`
				Reviewers     []string `json:"reviewers"`
				TeamReviewers []string `json:"team_reviewers"`
			}
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			repo, err := allowed.chooseRepository(arguments.Repository)
			if err != nil {
				return nil, err
			}
			if len(arguments.Reviewers)+len(arguments.TeamReviewers) < 1 {
				return nil, invalidArgument("pass reviewers, team_reviewers, or both with at least one name")
			}
			body := map[string]any{}
			for name, list := range map[string][]string{"reviewers": arguments.Reviewers,
				"team_reviewers": arguments.TeamReviewers} {
				if len(list) == 0 {
					continue
				}
				if len(list) > maxReviewerIDs {
					return nil, invalidArgument(name + " takes at most 20 names")
				}
				seen := map[string]bool{}
				for _, entry := range list {
					if !validName(entry) || seen[strings.ToLower(entry)] {
						return nil, invalidArgument(name + " takes distinct user or team names")
					}
					seen[strings.ToLower(entry)] = true
				}
				body[name] = list
			}
			if len(arguments.TeamReviewers) > 0 && !allowed.teamsAllowed(repo.owner) {
				return nil, invalidArgument("team_reviewers needs the organization of the repository among the " +
					"connection's targets as orgs/NAME, or targets that are empty")
			}
			op := action + " pull request reviewers"
			return onPull(allowed, arguments.Repository, arguments.Number, op,
				func(ctx context.Context, c *Client, repo target, path string, _ object) (any, error) {
					if action == "remove" {
						if err := c.change(ctx, op, method, path+"/requested_reviewers", body, nil); err != nil {
							return nil, err
						}
						return map[string]any{"number": arguments.Number, "done": true}, nil
					}
					var rows []object
					if err := c.change(ctx, op, method, path+"/requested_reviewers", body, &rows); err != nil {
						return nil, err
					}
					reviews := make([]map[string]any, 0, len(rows))
					for _, row := range rows {
						if row != nil {
							reviews = append(reviews, project(p, reviewMembers, row))
						}
					}
					return map[string]any{"number": arguments.Number, "reviews": reviews}, nil
				})
		},
	}
}

var (
	reviewersRequest = reviewersChange("request", http.MethodPost, "Request", "for", capability.EffectCreate,
		capability.IdempotencyNonIdempotent)
	reviewersRemove = reviewersChange("remove", http.MethodDelete, "Remove", "from", capability.EffectDelete,
		capability.IdempotencyIdempotent)
)
