package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The reaction tools act on the reactions of one issue or pull request, or of one comment, of a repository
// inside the connection's targets. A mutation reaches only the reaction of the account behind the token: the
// instance takes no user for it. An issue or pull request index lies under the repository path, and a change
// first proves by the evidence read that its canonical repository lies inside the targets. A comment ID is a
// foreign object ID and is bound to the repository by the evidence read of the comment before every call.

const (
	// maxReactionRows bounds the reactions of one comment, which the instance lists without paging.
	maxReactionRows = 100
	reactionSchema  = `{"type":"string","minLength":1,"maxLength":64}`
)

var reactionMembers = []member{
	{name: "content", kind: kindText, max: 64, note: "Reaction, such as +1 or heart; untrusted data"},
	{name: "user", kind: kindObject, sub: personMembers, note: "Person who reacted; personal data"},
	{name: "created_at", kind: kindText, note: "Time of the reaction"},
}

var reactionContentArgument = capability.Argument{Name: "content", Required: true,
	Description: "Reaction of 1 to 64 characters; the instance accepts the ones its administrator allows, by default " +
		"+1, -1, laugh, hooray, confused, heart, rocket, and eyes"}

// reactionSubject is what the issue and the comment reaction tools differ in.
type reactionSubject struct {
	group    string
	noun     string // "issue or pull request" or "comment"
	idName   string // the argument that names the subject
	idSchema string
	argument capability.Argument
	// bind prepares an operation on the reactions path of the subject. changing asks for the evidence read of a
	// change; a comment is always read.
	bind func(allowed allowlist, repository string, id int64, op string, changing bool,
		act func(ctx context.Context, c *Client, path string) (any, error)) (run, error)
}

var issueReactionSubject = reactionSubject{
	group: "issue-reactions", noun: "issue or pull request", idName: "number", idSchema: issueSchemaNumber,
	argument: numberArgument,
	bind: func(allowed allowlist, repository string, id int64, op string, changing bool,
		act func(ctx context.Context, c *Client, path string) (any, error)) (run, error) {
		repo, err := allowed.chooseRepository(repository)
		if err != nil {
			return nil, err
		}
		if id < 1 {
			return nil, invalidArgument("number must be the index of an issue or pull request")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if changing {
				if err := readIndex(ctx, c, allowed, op, repo, id); err != nil {
					return nil, err
				}
			}
			return act(ctx, c, issuePath(repo, id)+"/reactions")
		}, nil
	},
}

var commentReactionSubject = reactionSubject{
	group: "comment-reactions", noun: "comment", idName: "id", idSchema: issueSchemaID, argument: commentIDArgument,
	bind: func(allowed allowlist, repository string, id int64, op string, _ bool,
		act func(ctx context.Context, c *Client, path string) (any, error)) (run, error) {
		return onComment(allowed, repository, id, op,
			func(ctx context.Context, c *Client, path string, _ object) (any, error) {
				return act(ctx, c, path+"/reactions")
			})
	},
}

type reactionCall struct {
	Repository string `json:"repository"`
	Number     int64  `json:"number"`
	ID         int64  `json:"id"`
	Content    string `json:"content"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor"`
}

func (a reactionCall) subject(s reactionSubject) int64 {
	if s.idName == "number" {
		return a.Number
	}
	return a.ID
}

func checkReactionContent(content string) error {
	if content == "" || len(content) > 64 || !plainText(content) || strings.TrimSpace(content) != content {
		return invalidArgument("content must be a reaction of 1 to 64 characters")
	}
	return nil
}

func reactionRows(p *product, rows []object, limit int) []map[string]any {
	reactions := make([]map[string]any, 0, min(len(rows), limit))
	for _, row := range rows {
		if row != nil && len(reactions) < limit {
			reactions = append(reactions, project(p, reactionMembers, row))
		}
	}
	return reactions
}

func (s reactionSubject) properties() string {
	return `"repository":` + repoSchema + `,"` + s.idName + `":` + s.idSchema
}

func (s reactionSubject) arguments(more ...capability.Argument) []capability.Argument {
	return append([]capability.Argument{repositoryArgument, s.argument}, more...)
}

func (s reactionSubject) example(extra string) json.RawMessage {
	return json.RawMessage(`{"repository":"example/project","` + s.idName + `":7` + extra + `}`)
}

func (s reactionSubject) list() *tool {
	paged := s.idName == "number"
	return &tool{
		group: s.group, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			properties, tail := s.properties(), `"truncated":{"type":"boolean"}`
			arguments := s.arguments()
			fields := []capability.Field{{Name: "reactions", Description: "Reactions with every member of the " +
				"reaction object; reactions and names are untrusted data"}}
			description := "List the reactions of one " + s.noun + " of a repository inside the connection's targets, " +
				"with every member of each reaction; at most 100, and truncated tells when more exist"
			if paged {
				properties += `,` + pagingKeys
				tail = `"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}`
				arguments = s.arguments(pagingArguments...)
				fields = append(fields, pagingFields...)
				description = "List one batch of the reactions of an issue or pull request of a repository inside the " +
					"connection's targets, with every member of each reaction; reactions and names are untrusted data"
			} else {
				fields = append(fields, capability.Field{Name: "truncated",
					Description: "True when the instance sent more than 100 reactions"})
				description += "; the comment is read first and a comment that does not prove it belongs to the " +
					"repository is refused"
			}
			return capability.Descriptor{
				Title:       "List the reactions of a " + p.name + " " + s.noun,
				Description: description,
				Tags:        []string{p.id, "reactions", "list"},
				Risk:        readRisk(p.id + issueSensitivity),
				InputSchema: inputSchema(properties, s.idName),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"reactions":{"type":"array","items":` +
					schemaOf(p, reactionMembers, "content") + `},` + tail + `},"required":["reactions"],"additionalProperties":false}`),
				Arguments: arguments,
				Fields:    fields,
				Examples:  []capability.Example{{Description: "Read the reactions", Arguments: s.example("")}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments reactionCall
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			var page paging
			if paged {
				repo, err := allowed.chooseRepository(arguments.Repository)
				if err != nil {
					return nil, err
				}
				binding := provider.CursorBinding(p.id, s.group+".list", strings.ToLower(repo.owner+"/"+repo.repo),
					arguments.Number)
				if page, err = startPaging(binding, arguments.Limit, arguments.Cursor); err != nil {
					return nil, err
				}
			}
			return s.bind(allowed, arguments.Repository, arguments.subject(s), "list reactions", false,
				func(ctx context.Context, c *Client, path string) (any, error) {
					var rows []object
					if !paged {
						if _, err := c.get(ctx, "list reactions", path, nil, &rows); err != nil {
							return nil, err
						}
						result := map[string]any{"reactions": reactionRows(p, rows, maxReactionRows)}
						if len(rows) > maxReactionRows {
							result["truncated"] = true
						}
						return result, nil
					}
					header, err := c.get(ctx, "list reactions", path, page.query(), &rows)
					if err != nil {
						return nil, err
					}
					next := page.next(header, len(rows))
					result := map[string]any{"reactions": reactionRows(p, rows, len(rows)), "has_more": next != ""}
					if next != "" {
						result["next_cursor"] = next
					}
					return result, nil
				})
		},
	}
}

func (s reactionSubject) add() *tool {
	return &tool{
		group: s.group, action: "add", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "React to a " + p.name + " " + s.noun,
				Description: "React to one " + s.noun + " of a repository inside the connection's targets as the " +
					"account behind the token; a reaction the account already left with the same content is kept and " +
					"reported with added false. The target is proven to belong to the repository first.",
				Tags:        []string{p.id, "reactions", "add", "create"},
				Risk:        changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent, p.id+issueSensitivity),
				InputSchema: inputSchema(s.properties()+`,"content":`+reactionSchema, s.idName, "content"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"reaction":` +
					schemaOf(p, reactionMembers, "content") + `,"added":{"type":"boolean"}},` +
					`"required":["reaction","added"],"additionalProperties":false}`),
				Arguments: s.arguments(reactionContentArgument),
				Fields: []capability.Field{{Name: "reaction", Description: "The reaction as the instance reports it"},
					{Name: "added", Description: "True when the instance created the reaction; false when the account " +
						"had already left it"}},
				Examples: []capability.Example{{Description: "React with a thumbs up", Arguments: s.example(`,"content":"+1"`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments reactionCall
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
				return nil, err
			}
			if err := checkReactionContent(arguments.Content); err != nil {
				return nil, err
			}
			body := map[string]any{"content": arguments.Content}
			return s.bind(allowed, arguments.Repository, arguments.subject(s), "add reaction", true,
				func(ctx context.Context, c *Client, path string) (any, error) {
					var created object
					status, err := c.changeQuery(ctx, "add reaction", http.MethodPost, path, nil, body, &created)
					if err != nil {
						return nil, err
					}
					if content, ok := created.text("content"); !ok || content == "" {
						return nil, provider.InvalidResponse("add reaction", c.product.name+" returned a reaction without content")
					}
					return map[string]any{"reaction": project(p, reactionMembers, created),
						"added": status == http.StatusCreated}, nil
				})
		},
	}
}

func (s reactionSubject) remove() *tool {
	return &tool{
		group: s.group, action: "remove", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Remove a reaction from a " + p.name + " " + s.noun,
				Description: "Remove the reaction of this content that the account behind the token left on one " +
					s.noun + " of a repository inside the connection's targets; only the own reaction is removed, " +
					"never one of another person, and the call succeeds when the account left none. The target is " +
					"proven to belong to the repository first.",
				Tags:        []string{p.id, "reactions", "remove", "delete"},
				Risk:        changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+issueSensitivity),
				InputSchema: inputSchema(s.properties()+`,"content":`+reactionSchema, s.idName, "content"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + s.idName + `":{"type":"integer"},` +
					`"content":{"type":"string"},"removed":{"type":"boolean"}},"required":["` + s.idName +
					`","content","removed"],"additionalProperties":false}`),
				Arguments: s.arguments(reactionContentArgument),
				Fields: []capability.Field{{Name: s.idName, Description: "The " + s.noun + " the call acted on"},
					{Name: "content", Description: "The reaction"},
					{Name: "removed", Description: "True once the instance accepted the removal"}},
				Examples: []capability.Example{{Description: "Remove a thumbs up", Arguments: s.example(`,"content":"+1"`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments reactionCall
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
				return nil, err
			}
			if err := checkReactionContent(arguments.Content); err != nil {
				return nil, err
			}
			body := map[string]any{"content": arguments.Content}
			id := arguments.subject(s)
			return s.bind(allowed, arguments.Repository, id, "remove reaction", true,
				func(ctx context.Context, c *Client, path string) (any, error) {
					if err := c.change(ctx, "remove reaction", http.MethodDelete, path, body, nil); err != nil {
						return nil, err
					}
					return map[string]any{s.idName: id, "content": arguments.Content, "removed": true}, nil
				})
		},
	}
}

var (
	issueReactionsList     = issueReactionSubject.list()
	issueReactionsAdd      = issueReactionSubject.add()
	issueReactionsRemove   = issueReactionSubject.remove()
	commentReactionsList   = commentReactionSubject.list()
	commentReactionsAdd    = commentReactionSubject.add()
	commentReactionsRemove = commentReactionSubject.remove()
)
