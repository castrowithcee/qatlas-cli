package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var channelAttributionSentOnce = sentOnceFor("attribution setting", "matomo.channel-attribution.get with kind goals")

var channelAttributionSet = capability.Descriptor{
	ID: Provider + ".channel-attribution.set", Version: 1, Title: "Enable or disable Matomo goal attribution",
	Description: "Turn the multi-channel attribution of one goal of a website on or off " +
		"(MultiChannelConversionAttribution.setGoalAttribution). One read of the goal of the website proves idGoal " +
		"first; a goal the website does not have is not found and nothing is changed. Needs the premium plugin " +
		"MultiChannelConversionAttribution and a write right on the website (expected)" + channelAttributionSentOnce,
	Tags: []string{"matomo", "channel-attribution", "goals", "attribution", "update"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: channelAttributionSensitive},
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idGoalSchema + `,` +
		`"isEnabled":{"type":"boolean"}},"required":["idGoal","isEnabled"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
		`"isEnabled":{"type":"boolean"},"updated":{"type":"boolean"}},` +
		`"required":["idSite","idGoal","isEnabled","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), idGoalArgument("change"),
		{Name: "isEnabled", Required: true, Description: "True turns the attribution of the goal on, false turns it off"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the goal belongs to"},
		{Name: "idGoal", Description: "The goal that was changed"},
		{Name: "isEnabled", Description: "The setting Matomo accepted"},
		{Name: "updated", Description: "True once Matomo accepted the change"}},
	Examples: []capability.Example{{Description: "Turn attribution on for goal 4",
		Arguments: json.RawMessage(`{"idSite":1,"idGoal":4,"isEnabled":true}`)}},
}

// ChannelAttributionSet is the answer of channel-attribution.set.
type ChannelAttributionSet struct {
	IDSite    int  `json:"idSite"`
	IDGoal    int  `json:"idGoal"`
	IsEnabled bool `json:"isEnabled"`
	Updated   bool `json:"updated"`
}

func invokeChannelAttributionSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set goal attribution"
	var args struct {
		IDSite    *int  `json:"idSite"`
		IDGoal    *int  `json:"idGoal"`
		IsEnabled *bool `json:"isEnabled"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IsEnabled == nil {
		return nil, invalidArgument("isEnabled is required")
	}
	goalID, err := goalIDOf(args.IDGoal)
	if err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := channelAttributionRequirement(channelAttributionSet.ID)
	// An ID alone proves nothing for the website: one read of the goal binds it before anything is changed.
	if _, err := client.fetchGoal(ctx, op, req, site, goalID); err != nil {
		return nil, err
	}
	// Assumed: like the other flags of Matomo's core API, isEnabled is sent as 1 or 0.
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idGoal": {strconv.Itoa(goalID)},
		"isEnabled": {flagValue(args.IsEnabled)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "MultiChannelConversionAttribution.setGoalAttribution", params,
		channelAttributionSentOnce, &answer); err != nil {
		return nil, err
	}
	var confirmed bool
	if !isSuccessAnswer(answer) && (scalar(answer, &confirmed) != nil || !confirmed) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the attribution change", channelAttributionSentOnce)
	}
	return &ChannelAttributionSet{IDSite: site, IDGoal: goalID, IsEnabled: *args.IsEnabled, Updated: true}, nil
}

func channelAttributionSetOperations() []capability.Operation {
	return []capability.Operation{{Descriptor: channelAttributionSet, Handler: capability.Handler(invokeChannelAttributionSet)}}
}
