package forge

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The version tool reads the product version the instance reports below /api/v1. It is instance metadata, not
// data of an owner or repository, so every connection offers it whatever its targets.

var versionGet = &tool{
	group: "version", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title:       "Get the " + p.name + " version",
			Description: "Read the version string the " + p.name + " instance reports",
			Tags:        []string{p.id, "instance", "version", "get"},
			Risk:        readRisk(p.id + "-instance-metadata"),
			InputSchema: inputSchema(""),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"version":{"type":"string"}},` +
				`"required":["version"],"additionalProperties":false}`),
			Fields: []capability.Field{{Name: "version", Description: "Version string as the instance reports it, " +
				"untrusted data"}},
			Examples: []capability.Example{{Description: "Read the version", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, _ allowlist, _ json.RawMessage) (run, error) {
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get version"
			var answer struct {
				Version string `json:"version"`
			}
			if _, err := c.get(ctx, op, "/version", nil, &answer); err != nil {
				return nil, err
			}
			if answer.Version == "" {
				return nil, provider.InvalidResponse(op, p.name+" returned an invalid version")
			}
			return map[string]any{"version": bounded(answer.Version)}, nil
		}, nil
	},
}
