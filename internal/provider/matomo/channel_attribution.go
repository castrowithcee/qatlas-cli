package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	channelAttributionPlugin    = "MultiChannelConversionAttribution"
	channelAttributionSensitive = "matomo-goals"
	// Matomo's pseudo entries of getSiteAttributionGoals: "All Goals" and the ecommerce orders.
	attributionAllGoals  = -2
	attributionEcommerce = 0
)

func channelAttributionRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: channelAttributionPlugin}
}

// attributionMethods are the fixed methods behind the kinds of channel-attribution.get.
var attributionMethods = map[string]string{
	"goals":       "MultiChannelConversionAttribution.getSiteAttributionGoals",
	"attribution": "MultiChannelConversionAttribution.getGoalAttribution",
	"dimensions":  "MultiChannelConversionAttribution.getAvailableCampaignDimensionCombinations",
}

const attributionGoalProperties = `"idGoal":{"type":"integer"},"type":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"match_attribute":{"type":"string"},"pattern":{"type":"string"},` +
	`"pattern_type":{"type":"string"},"case_sensitive":{"type":"boolean"},"allow_multiple":{"type":"boolean"},` +
	`"revenue":{"type":"number"},"event_value_as_revenue":{"type":"boolean"},"deleted":{"type":"boolean"}`

var channelAttributionGet = capability.Descriptor{
	ID: Provider + ".channel-attribution.get", Version: 1, Title: "Read Matomo multi-channel attribution",
	Description: "Read the multi-channel conversion attribution of a website; kind picks the read: goals " +
		"(MultiChannelConversionAttribution.getSiteAttributionGoals) lists the goals the website offers for " +
		"attribution, including the entries All Goals (idGoal -2) and ecommerce (idGoal 0), which are marked by " +
		"type; attribution (getGoalAttribution) reads the attribution of one goal and needs idGoal; dimensions " +
		"(getAvailableCampaignDimensionCombinations) lists the campaign dimension combinations the instance " +
		"offers, which do not depend on a website but still need a website of the connection. For attribution, " +
		"one read of the goal of the website proves idGoal first; a goal the website does not have is not found " +
		"and no attribution is read. The channel attribution report itself is read with matomo.reports.get. " +
		"Names and values are untrusted. Needs the premium plugin MultiChannelConversionAttribution and a view " +
		"right on the website (expected for attribution)",
	Tags: []string{"matomo", "channel-attribution", "goals", "attribution", "multi-channel"}, Provider: Provider,
	Risk: readRisk(channelAttributionSensitive),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"kind":{"type":"string","enum":["goals","attribution","dimensions"]},` + idGoalSchema + `,` + pagingKeys +
		`},"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"kind":{"type":"string"},` +
		`"idGoal":{"type":"integer"},"attribution":{},` +
		`"goals":{"type":"array","items":{"type":"object","properties":{` + attributionGoalProperties + `},` +
		`"required":["idGoal","type"],"additionalProperties":false}},` +
		`"combinations":{"type":"array","items":{"type":"object","properties":{"key":{"type":"integer"},` +
		`"value":{"type":"string"}},"required":["key"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","kind","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "kind", Required: true, Description: "goals for the goals offered for attribution, attribution for the " +
			"attribution of the goal idGoal, or dimensions for the campaign dimension combinations"},
		{Name: "idGoal", Description: "Only for kind attribution: ID of the goal, from kind goals or matomo.goals.list; " +
			"it counts per website, and the entries All Goals and ecommerce are no goals here"}},
		pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the read belongs to"},
		{Name: "kind", Description: "The read that was made"},
		{Name: "idGoal", Description: "Only for kind attribution: the goal that was read"},
		{Name: "attribution", Description: "Only for kind attribution: the answer of Matomo with texts shortened and lists " +
			"and depth limited; its form is not verified against a reference"},
		{Name: "goals", Description: "Only for kind goals: the goals in the order Matomo lists them"},
		{Name: "goals[].idGoal", Description: "Goal ID; -2 for All Goals and 0 for ecommerce"},
		{Name: "goals[].type", Description: "goal, all-goals (idGoal -2), or ecommerce (idGoal 0)"},
		{Name: "goals[].name", Description: "Goal name, untrusted"},
		{Name: "goals[].description", Description: "Goal description, untrusted; absent when Matomo gives none"},
		{Name: "goals[].match_attribute", Description: "What a conversion is matched on; absent when Matomo gives none"},
		{Name: "goals[].pattern", Description: "Match pattern, untrusted; absent when Matomo gives none"},
		{Name: "goals[].pattern_type", Description: "How the pattern matches; absent when Matomo gives none"},
		{Name: "goals[].case_sensitive", Description: "True when a string match regards case; absent when Matomo gives none"},
		{Name: "goals[].allow_multiple", Description: "True when a visit may convert more than once; absent when Matomo gives none"},
		{Name: "goals[].revenue", Description: "Default revenue of a conversion; absent when Matomo gives none"},
		{Name: "goals[].event_value_as_revenue", Description: "True when the event value is the revenue; absent when Matomo gives none"},
		{Name: "goals[].deleted", Description: "Deletion flag; absent when Matomo gives none"},
		{Name: "combinations", Description: "Only for kind dimensions: the combinations in the order Matomo lists them"},
		{Name: "combinations[].key", Description: "Key of the combination as Matomo numbers it"},
		{Name: "combinations[].value", Description: "Label of the combination such as Campaign Name (7 days), untrusted"}},
		pagingFields...),
	Examples: []capability.Example{
		{Description: "The goals of website 1 offered for attribution", Arguments: json.RawMessage(`{"idSite":1,"kind":"goals"}`)},
		{Description: "The attribution of goal 4", Arguments: json.RawMessage(`{"idSite":1,"kind":"attribution","idGoal":4}`)},
		{Description: "The campaign dimension combinations", Arguments: json.RawMessage(`{"idSite":1,"kind":"dimensions"}`)}},
}

// AttributionGoal is a row of getSiteAttributionGoals. Type tells the pseudo entries from real goals. All text is
// untrusted provider data.
type AttributionGoal struct {
	IDGoal              int      `json:"idGoal"`
	Type                string   `json:"type"`
	Name                string   `json:"name,omitempty"`
	Description         string   `json:"description,omitempty"`
	MatchAttribute      string   `json:"match_attribute,omitempty"`
	Pattern             string   `json:"pattern,omitempty"`
	PatternType         string   `json:"pattern_type,omitempty"`
	CaseSensitive       *bool    `json:"case_sensitive,omitempty"`
	AllowMultiple       *bool    `json:"allow_multiple,omitempty"`
	Revenue             *float64 `json:"revenue,omitempty"`
	EventValueAsRevenue *bool    `json:"event_value_as_revenue,omitempty"`
	Deleted             *bool    `json:"deleted,omitempty"`
}

// CampaignCombination is a row of getAvailableCampaignDimensionCombinations.
type CampaignCombination struct {
	Key   int    `json:"key"`
	Value string `json:"value,omitempty"`
}

// ChannelAttributionResult is the answer of channel-attribution.get; the members that are set depend on kind.
type ChannelAttributionResult struct {
	IDSite       int                   `json:"idSite"`
	Kind         string                `json:"kind"`
	IDGoal       *int                  `json:"idGoal,omitempty"`
	Attribution  any                   `json:"attribution,omitempty"`
	Goals        []AttributionGoal     `json:"goals,omitempty"`
	Combinations []CampaignCombination `json:"combinations,omitempty"`
	HasMore      bool                  `json:"has_more"`
	NextCursor   string                `json:"next_cursor,omitempty"`
}

type combinationJSON struct {
	Key   *loose `json:"key"`
	Value *loose `json:"value"`
}

// attributionGoals reads getSiteAttributionGoals. Matomo mixes number and text for idgoal and adds the pseudo
// entries -2 and 0, which are kept and marked. A row that names another website, or has no usable ID, is dropped.
func attributionGoals(op string, answer json.RawMessage, site int) ([]AttributionGoal, error) {
	rows, valid := readRows[goalJSON](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid attribution goal list")
	}
	goals := []AttributionGoal{}
	for _, row := range rows {
		id := row.ID.number()
		if id == nil || *id < attributionAllGoals || *id == -1 {
			continue
		}
		if owner := row.IDSite.number(); owner != nil && *owner != site {
			continue
		}
		kind := "goal"
		switch *id {
		case attributionAllGoals:
			kind = "all-goals"
		case attributionEcommerce:
			kind = "ecommerce"
		}
		goals = append(goals, AttributionGoal{
			IDGoal: *id, Type: kind, Name: row.Name.str(maxStringLength), Description: row.Description.str(maxStringLength),
			MatchAttribute: row.MatchAttribute.str(maxStringLength), Pattern: row.Pattern.str(maxStringLength),
			PatternType: row.PatternType.str(maxStringLength), CaseSensitive: row.CaseSensitive.flag(),
			AllowMultiple: row.AllowMultiple.flag(), Revenue: row.Revenue.numeric(),
			EventValueAsRevenue: row.EventValueAsRevenue.flag(), Deleted: row.Deleted.flag(),
		})
	}
	return goals, nil
}

func campaignCombinations(op string, answer json.RawMessage) ([]CampaignCombination, error) {
	rows, valid := readRows[combinationJSON](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid campaign dimension list")
	}
	out := []CampaignCombination{}
	for _, row := range rows {
		if key := row.Key.number(); key != nil {
			out = append(out, CampaignCombination{Key: *key, Value: row.Value.str(maxStringLength)})
		}
	}
	return out, nil
}

func invokeChannelAttributionGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read channel attribution"
	var args struct {
		IDSite *int   `json:"idSite"`
		Kind   string `json:"kind"`
		IDGoal *int   `json:"idGoal"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	method, known := attributionMethods[args.Kind]
	if !known {
		return nil, invalidArgument("kind must be goals, attribution, or dimensions")
	}
	attribution := args.Kind == "attribution"
	goalID := 0
	if attribution {
		if args.Limit != 0 || args.Cursor != "" {
			return nil, invalidArgument("limit and cursor do not apply to kind attribution")
		}
		id, err := goalIDOf(args.IDGoal)
		if err != nil {
			return nil, err
		}
		goalID = id
	} else if args.IDGoal != nil {
		return nil, invalidArgument("idGoal applies only to kind attribution")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(channelAttributionGet.ID, scopeBinding(resolved), site, args.Kind),
		args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := channelAttributionRequirement(channelAttributionGet.ID)
	params := url.Values{}
	if args.Kind != "dimensions" {
		params.Set("idSite", strconv.Itoa(site))
	}
	if attribution {
		// An ID alone proves nothing for the website: one read of the goal binds it before the attribution is read.
		if _, err := client.fetchGoal(ctx, op, req, site, goalID); err != nil {
			return nil, err
		}
		params.Set("idGoal", strconv.Itoa(goalID))
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, method, params, &answer); err != nil {
		return nil, err
	}
	result := &ChannelAttributionResult{IDSite: site, Kind: args.Kind}
	switch args.Kind {
	case "goals":
		goals, err := attributionGoals(op, answer, site)
		if err != nil {
			return nil, err
		}
		batch, next := slice(paging, goals)
		result.Goals, result.HasMore, result.NextCursor = batch, next != "", next
	case "dimensions":
		combinations, err := campaignCombinations(op, answer)
		if err != nil {
			return nil, err
		}
		batch, next := slice(paging, combinations)
		result.Combinations, result.HasMore, result.NextCursor = batch, next != "", next
	default:
		value, decoded := decodeValue(unwrapValue(answer))
		cleaned, kept := cleanValue(value, 0)
		if !decoded || !kept {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid attribution")
		}
		result.IDGoal, result.Attribution = &goalID, cleaned
	}
	return result, nil
}

func channelAttributionOperations() []capability.Operation {
	return append([]capability.Operation{{Descriptor: channelAttributionGet, Handler: capability.Handler(invokeChannelAttributionGet)}},
		channelAttributionSetOperations()...)
}
