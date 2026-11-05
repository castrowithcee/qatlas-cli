package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// maxRepositoryTopics is the number of topics GitHub accepts on one repository.
const maxRepositoryTopics = 20

// topicPattern is GitHub's rule for one topic: lowercase letters, digits, and hyphens, starting with a letter
// or a digit, at most 50 characters.
var topicPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)

const repositoryTopicsPermission = "GitHub refused this change of the repository topics or does not show " +
	"the repository to this token; it needs the admin role on the repository with repo on a classic token, or " +
	"Administration: write on a fine-grained token, and an organization policy may forbid it as well"

var repositoryTopicsSet = capability.Descriptor{
	ID:      Provider + ".repository-topics.set",
	Version: 1,
	Title:   "Replace the topics of a GitHub repository",
	Description: "Replace the whole topic list of a repository a connection allows with the given one, at most " +
		"20 topics; an empty list removes all of them. Offered only where the connection lists it",
	Tags:                  []string{"github", "repository", "topics", "update", "admin"},
	Risk:                  guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, repositorySettingSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           inputSchema(`"names":{"type":"array","maxItems":20,"items":{"type":"string"}}`, "names"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"topics":{"type":"array",` +
		`"items":{"type":"string"}},"repository":{"type":"string"}},"required":["topics","repository"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "names", Description: "Complete new topic list, 0 to 20 distinct topics of lowercase letters, " +
			"digits, and hyphens, each starting with a letter or digit and at most 50 characters", Required: true},
	},
	Fields: []capability.Field{
		{Name: "topics", Description: "Topics GitHub reports after the change" + untrustedText},
	},
	Examples: []capability.Example{{
		Description: "Set the topics of a repository",
		Arguments:   json.RawMessage(`{"names":["go","cli"]}`),
	}},
}

func checkRepositoryTopics(a *actionsArguments, _ target) error {
	if a.Names == nil {
		return invalidRequest("names must be a list of topics, which may be empty")
	}
	if len(a.Names) > maxRepositoryTopics {
		return invalidRequest("names holds at most 20 topics")
	}
	seen := map[string]bool{}
	for _, name := range a.Names {
		if !topicPattern.MatchString(name) {
			return invalidRequest("every topic must be 1 to 50 lowercase letters, digits, or hyphens, starting " +
				"with a letter or digit")
		}
		if seen[name] {
			return invalidRequest("names must not repeat a topic")
		}
		seen[name] = true
	}
	return nil
}

// RepositoryTopics is what repository-topics.set reports: the topics GitHub holds now.
type RepositoryTopics struct {
	Topics []string `json:"topics"`
}

// setRepositoryTopics replaces the topics in one request that is never repeated.
func (c *Client) setRepositoryTopics(ctx context.Context, names []string) (*RepositoryTopics, error) {
	const op = "set repository topics"
	path := "/repos/" + url.PathEscape(c.target.owner) + "/" + url.PathEscape(c.target.repo) + "/topics"
	var answer struct {
		Names []string `json:"names"`
	}
	if err := c.restChange(ctx, op, http.MethodPut, path, map[string][]string{"names": names}, &answer); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassProviderError && failure.Message == rejectedMessage {
			return nil, invalidRequest("GitHub rejected these topics")
		}
		return nil, repositorySettingsFailure(err, repositoryTopicsPermission)
	}
	// The reported list must hold exactly the topics that were sent, in any order; otherwise the state is unclear.
	if len(answer.Names) != len(names) {
		return nil, invalidResponse(op, true)
	}
	sent := map[string]bool{}
	for _, name := range names {
		sent[name] = true
	}
	for _, name := range answer.Names {
		// Deleting on a hit makes a repeated entry count once, so it cannot hide a missing one.
		if !sent[name] {
			return nil, invalidResponse(op, true)
		}
		delete(sent, name)
	}
	if answer.Names == nil {
		answer.Names = []string{}
	}
	return &RepositoryTopics{Topics: answer.Names}, nil
}
