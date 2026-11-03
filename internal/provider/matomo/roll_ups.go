package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	rollUpReportingPlugin = "RollUpReporting"
	maxRollUpName         = 255
	maxRollUpSources      = 20
)

func rollUpsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: rollUpReportingPlugin}
}

var rollUpsList = capability.Descriptor{
	ID: Provider + ".roll-ups.list", Version: 1, Title: "List Matomo roll-up websites",
	Description: "Read the roll-up websites of the instance (getRollUps) with their name, time zone, currency, and " +
		"source websites. Roll-ups need Super User rights and a connection whose targets name the instance. Only " +
		"roll-ups whose own website lies inside the connection's website targets are returned, and their source " +
		"websites are cut to the websites inside the targets, so a roll-up can show fewer sources than it has. " +
		"Names are untrusted. The answer of Matomo is not verified against a reference. Needs the premium plugin " +
		"RollUpReporting",
	Tags: []string{"matomo", "roll-ups", "superuser", "instance", "list"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"rollUps":{"type":"array","items":{"type":"object",` +
		`"properties":{"idSite":{"type":"integer"},"name":{"type":"string"},"timezone":{"type":"string"},` +
		`"currency":{"type":"string"},"sourceIdSites":{"type":"array","items":{"type":"integer"}}},` +
		`"required":["idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["has_more"],"additionalProperties":false}`),
	Arguments: pagingArguments,
	Fields: append([]capability.Field{
		{Name: "rollUps", Description: "Roll-ups inside the connection's targets: idSite, name (untrusted), timezone, " +
			"currency, and sourceIdSites (only the sources inside the targets)"}}, pagingFields...),
	Examples: []capability.Example{{Description: "The roll-up websites", Arguments: json.RawMessage(`{}`)}},
}

// RollUp is one roll-up website of the list. The name is untrusted provider data.
type RollUp struct {
	IDSite        int    `json:"idSite"`
	Name          string `json:"name,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	Currency      string `json:"currency,omitempty"`
	SourceIDSites []int  `json:"sourceIdSites,omitempty"`
}

// RollUpsResult is the answer of roll-ups.list.
type RollUpsResult struct {
	RollUps    []RollUp `json:"rollUps,omitempty"`
	HasMore    bool     `json:"has_more"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// readRollUps reads the roll-up rows once. The member names follow the Matomo core convention for website rows
// (lower case idsite) and are matched without regard to case, as the reference gives no example.
func readRollUps(ctx context.Context, client *Client, op string, req requirement) ([]RollUp, error) {
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "RollUpReporting.getRollUps", url.Values{}, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[map[string]json.RawMessage](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid roll-up list")
	}
	out := make([]RollUp, 0, len(rows))
	for _, row := range rows {
		members := make(map[string]json.RawMessage, len(row))
		for key, value := range row {
			members[strings.ToLower(key)] = value
		}
		var id *loose
		if json.Unmarshal(members["idsite"], &id) != nil || id.number() == nil || *id.number() < 1 {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid roll-up list")
		}
		rollUp := RollUp{IDSite: *id.number()}
		for key, target := range map[string]*string{"name": &rollUp.Name, "timezone": &rollUp.Timezone, "currency": &rollUp.Currency} {
			var text *loose
			if json.Unmarshal(members[key], &text) == nil {
				*target = text.str(maxStringLength)
			}
		}
		var sources []json.RawMessage
		if json.Unmarshal(members["sourceidsites"], &sources) == nil {
			for _, source := range sources {
				var item *loose
				if json.Unmarshal(source, &item) == nil && item.number() != nil {
					rollUp.SourceIDSites = append(rollUp.SourceIDSites, *item.number())
				}
			}
		}
		out = append(out, rollUp)
	}
	return out, nil
}

func invokeRollUpsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list roll-ups"
	var args struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	paging, err := startPage(provider.CursorBinding(rollUpsList.ID, scopeBinding(resolved)), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	rows, err := readRollUps(ctx, client, op, rollUpsRequirement(rollUpsList.ID))
	if err != nil {
		return nil, err
	}
	var inside []RollUp
	for _, row := range rows {
		if !bound.allows(row.IDSite) {
			continue
		}
		var sources []int
		for _, id := range row.SourceIDSites {
			if bound.allows(id) {
				sources = append(sources, id)
			}
		}
		row.SourceIDSites = sources
		inside = append(inside, row)
	}
	batch, next := slice(paging, inside)
	return &RollUpsResult{RollUps: batch, HasMore: next != "", NextCursor: next}, nil
}

func rollUpsOperations() []capability.Operation {
	return append([]capability.Operation{{Descriptor: rollUpsList, Handler: capability.Handler(invokeRollUpsList)}},
		rollUpChangeOperations()...)
}
