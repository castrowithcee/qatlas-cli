// Package brave implements read-only access to the Brave Search API with one subscription key: LLM
// Context, news, video, and image search today, and the other search endpoints of the same API as further
// tools of this package.
//
// A connection binds one API key, sent only in the X-Subscription-Token header, and takes no target: a key
// reaches the endpoints of its plan, and the connection's effects and tools decide which of them an agent may
// call. The base URL is fixed to the official API origin; any other value is refused before the key is read.
//
// Brave counts requests per key in a sliding one-second window and in a monthly quota, and bills every
// successful request. The provider spaces the requests of one key, reads the X-RateLimit-* headers of every
// answer to slow itself down, and reports the remaining quota with each result. A request is sent once and
// never repeated.
//
// Everything Brave returns, titles, descriptions, snippets, and addresses alike, is untrusted third-party
// content: strings and lists are bounded, addresses are text that is never fetched, and nothing is cached or
// stored, because Brave's terms of use forbid keeping its results.
package brave

import (
	"context"
	"errors"
	"net/url"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "brave"

// roleToken is the single secret role: the Brave API key from the dashboard.
const roleToken = "token"

// operations lists the tools of the provider. A further Brave endpoint is one more entry here, one more
// entry of searchTools, and, where it is cheaper to probe than the others, one more entry of probes.
func operations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: llmContext, Handler: handle("get llm context", prepareLLMContext)},
		{Descriptor: newsSearch, Handler: handle("search news", prepareNewsSearch)},
		{Descriptor: videosSearch, Handler: handle("search videos", prepareVideosSearch)},
		{Descriptor: imagesSearch, Handler: handle("search images", prepareImagesSearch)},
	}
}

// searchTools are the tools of the search profile, the Search plan of Brave.
var searchTools = []string{llmContext.ID, newsSearch.ID, videosSearch.ID, imagesSearch.ID}

// probe is the smallest request that proves a key for one tool.
type probe struct {
	tool  string
	path  string
	query url.Values
}

// probes lists the endpoints a connection test may call, cheapest first. Brave offers no free key check, so
// the test spends one billed request on the first endpoint the connection offers.
var probes = []probe{
	{tool: "brave.news.search", path: newsPath, query: url.Values{"q": {"brave"}, "count": {"1"}}},
	{tool: llmContext.ID, path: llmPath,
		query: url.Values{"q": {"brave"}, "count": {"1"}, "maximum_number_of_urls": {"1"}}},
}

// probeFor returns the probe of the first tool the connection offers: by its tools list when it has one,
// otherwise by its read effect.
func probeFor(resolved *config.Resolved) (probe, bool) {
	for _, candidate := range probes {
		if resolved.Tools != nil {
			if slices.Contains(resolved.Tools, candidate.tool) {
				return candidate, true
			}
		} else if slices.Contains(resolved.Effects, config.EffectRead) {
			return candidate, true
		}
	}
	return probe{}, false
}

// TestConnection sends one real minimal search request. It spends at most one billed request, and a
// success proves that the key is valid for that endpoint only: it shows neither the plan nor any other
// endpoint. Brave answers an invalid request with 422 whether or not the key is right, so a 422 is never
// taken as proof of a valid key.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	const op = "test connection"
	if resolved == nil {
		return "", failure(op, "no connection was selected")
	}
	target, ok := probeFor(resolved)
	if !ok {
		return "", failure(op, "this connection offers no Brave tool, so there is no endpoint to test")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return testClass(err)
	}
	var answer map[string]any
	if _, err := client.get(ctx, op, target.path, target.query, nil, &answer); err != nil {
		return testClass(err)
	}
	return provider.ClassOK, nil
}

// testClass turns a provider failure into the class of a connection test; any other error is returned.
func testClass(err error) (provider.Class, error) {
	var failed *provider.Error
	if errors.As(err, &failed) {
		return failed.Class, nil
	}
	return "", err
}

// noTarget refuses every configured target. A key reaches the endpoints of its plan, not a part of the
// account, so a target would only suggest a limit that does not exist.
func noTarget(string) error {
	return errors.New("a Brave connection takes no target; leave the target empty")
}

// Register adds the provider metadata, its connection test, and its tools.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Brave Search", DefaultBaseURL: apiRoot,
		Description:    "Web, news, and AI-grounding search through the Brave Search API with a subscription key",
		DefaultEffects: []config.Effect{config.EffectRead},
		ValidateBaseURL: func(raw string) error {
			if !isAPIRoot(raw) {
				return errors.New("a Brave service must use the official API origin " + apiRoot)
			}
			return nil
		},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: "Brave Search API key from the API dashboard; the plans of the account decide " +
				"which endpoints it reaches, and every successful request is billed to it",
		}},
		// Brave has no targets. The label and description are what the editors show on the empty row;
		// Validate refuses any value, so a target never reaches a request.
		Target: config.TargetMetadata{
			Label:       "target",
			Description: "none: a Brave key reaches the endpoints of its plan, so leave the target empty",
			Validate:    noTarget,
		},
		Profiles: []config.ToolProfile{{
			ID: "search", Title: "Search", Recommended: true,
			Description: "searches the web for agents and searches news, videos, and images with the Search plan of Brave; every request is billed and nothing " +
				"is changed",
			Tools: searchTools,
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider, operations()...)
}
